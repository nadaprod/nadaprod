package studio

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"

	"nadaprod/pkg/export"
	"nadaprod/pkg/gemini"
	"nadaprod/pkg/i18n"
	"nadaprod/pkg/projects"
)

// Traduction du studio (doc/I18N.md) : la mémoire de traduction est indexée
// par le texte source ; les segments se recalculent à chaque lecture depuis
// les blocs courants, la génération n'a donc rien à préserver.

const (
	translateTimeout = 3 * time.Minute
	maxTranslateRun  = 200 // segments par appel ; le client relance tant qu'il en reste
)

// language : une langue proposée par le studio (nom natif).
type language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

var languages = []language{
	{"fr", "Français"}, {"en", "English"}, {"es", "Español"}, {"de", "Deutsch"}, {"it", "Italiano"},
	{"pt", "Português"}, {"pt-br", "Português (Brasil)"}, {"nl", "Nederlands"}, {"pl", "Polski"},
	{"sv", "Svenska"}, {"da", "Dansk"}, {"no", "Norsk"}, {"fi", "Suomi"}, {"cs", "Čeština"},
	{"ro", "Română"}, {"el", "Ελληνικά"}, {"tr", "Türkçe"}, {"ru", "Русский"}, {"uk", "Українська"},
	{"ar", "العربية"}, {"he", "עברית"}, {"ja", "日本語"}, {"ko", "한국어"}, {"zh", "中文"}, {"hi", "हिन्दी"},
}

// segment : un segment de la page et les blocs où il apparaît ("seo" pour
// le titre et la description).
type segment struct {
	i18n.Segment
	Blocks []string `json:"blocks"`
}

// entry : une traduction telle que l'UI la voit.
type entry struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// segmentsOf : les segments de la page, dans l'ordre, sans doublon.
func segmentsOf(p export.Page) []segment {
	var out []segment
	at := map[string]int{}
	add := func(s i18n.Segment, block string) {
		if i, ok := at[s.Hash]; ok {
			if !slices.Contains(out[i].Blocks, block) {
				out[i].Blocks = append(out[i].Blocks, block)
			}
			return
		}
		at[s.Hash] = len(out)
		out = append(out, segment{Segment: s, Blocks: []string{block}})
	}
	for _, text := range []string{p.SEO.Title, p.SEO.Description} {
		if s, ok := export.PlainSegment(text); ok {
			add(s, "seo")
		}
	}
	for _, b := range p.Blocks {
		for _, s := range i18n.Extract(b.HTML) {
			add(s, b.ID)
		}
	}
	return out
}

func registerI18n(api *gin.RouterGroup, d Deps, page func(*gin.Context) (export.Page, projects.Project, bool), throttled func(*gin.Context) bool) {
	// target : la page, ses segments et la langue cible de l'URL (une des
	// langues du projet).
	target := func(c *gin.Context) (export.Page, []segment, string, bool) {
		p, _, ok := page(c)
		if !ok {
			return p, nil, "", false
		}
		_, langs := p.Languages()
		lang := c.Param("lang")
		if !slices.Contains(langs, lang) {
			fail(c, http.StatusBadRequest, "invalid", "langue absente du projet : "+lang)
			return p, nil, "", false
		}
		return p, segmentsOf(p), lang, true
	}

	// État complet : segments + traductions (seulement celles des segments présents).
	api.GET("/projects/:id/i18n", func(c *gin.Context) {
		p, _, ok := page(c)
		if !ok {
			return
		}
		source, langs := p.Languages()
		segs := segmentsOf(p)
		all, err := d.Projects.Translations(c.Request.Context(), c.Param("id"))
		if err != nil {
			fail(c, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		tr := map[string]map[string]entry{}
		for _, l := range langs {
			tr[l] = map[string]entry{}
			for _, s := range segs {
				if t, ok := all[l][s.Hash]; ok {
					tr[l][s.Hash] = entry{t.Text, t.Status}
				}
			}
		}
		if segs == nil {
			segs = []segment{}
		}
		c.JSON(http.StatusOK, gin.H{"source": source, "langs": langs, "segments": segs, "translations": tr})
	})

	// Saisie humaine : {items:[{hash,text}]} ; text "" supprime.
	api.PUT("/projects/:id/i18n/:lang", func(c *gin.Context) {
		var body struct {
			Items []struct {
				Hash string `json:"hash"`
				Text string `json:"text"`
			} `json:"items"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			fail(c, http.StatusBadRequest, "invalid", "requête invalide : "+err.Error())
			return
		}
		_, segs, lang, ok := target(c)
		if !ok {
			return
		}
		src := map[string]string{}
		for _, s := range segs {
			src[s.Hash] = s.Src
		}
		ts := make([]projects.Translation, 0, len(body.Items))
		for _, it := range body.Items {
			s, known := src[it.Hash]
			if !known {
				fail(c, http.StatusBadRequest, "invalid", "segment inconnu : "+it.Hash)
				return
			}
			if it.Text != "" && !i18n.Valid(s, it.Text) {
				c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "les marqueurs <n>…</n> doivent rester identiques", "code": "invalid_markers", "hash": it.Hash})
				return
			}
			ts = append(ts, projects.Translation{Hash: it.Hash, Src: s, Text: it.Text, Status: projects.StatusHuman})
		}
		if err := d.Projects.SaveTranslations(c.Request.Context(), c.Param("id"), lang, ts); err != nil {
			projects.Fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Traduction automatique des manquants (ou des hashes donnés ; redo
	// reprend aussi les traductions machine). Une saisie humaine n'est jamais écrasée.
	api.POST("/projects/:id/i18n/:lang/translate", func(c *gin.Context) {
		var body struct {
			Hashes []string `json:"hashes"`
			Redo   bool     `json:"redo"`
		}
		_ = c.ShouldBindJSON(&body)
		if d.Translator == nil {
			fail(c, http.StatusServiceUnavailable, "no_translator", "traduction automatique indisponible")
			return
		}
		p, segs, lang, ok := target(c)
		if !ok {
			return
		}
		all, err := d.Projects.Translations(c.Request.Context(), c.Param("id"))
		if err != nil {
			fail(c, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		items, remaining := map[string]string{}, 0
		for _, s := range segs {
			t, has := all[lang][s.Hash]
			wanted := len(body.Hashes) == 0 || slices.Contains(body.Hashes, s.Hash)
			todo := !has || (body.Redo && t.Status == projects.StatusMachine)
			switch {
			case !wanted || !todo:
			case len(items) < maxTranslateRun:
				items[s.Hash] = s.Src
			default:
				remaining++
			}
		}
		out := map[string]entry{}
		failed := []string{}
		if len(items) > 0 {
			if throttled(c) {
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), translateTimeout)
			defer cancel()
			source, _ := p.Languages()
			got, err := d.Translator.Translate(ctx, gemini.TranslateRequest{
				From: source, To: lang, Items: items,
				Brand: gemini.Brand{Name: p.Brand.Name, Tone: p.Brand.Tone},
			})
			ts := []projects.Translation{}
			for h, src := range items {
				if t, ok := got[h]; ok && i18n.Valid(src, t) {
					ts = append(ts, projects.Translation{Hash: h, Src: src, Text: t, Status: projects.StatusMachine})
					out[h] = entry{t, projects.StatusMachine}
				} else {
					failed = append(failed, h)
				}
			}
			// Ce qui est arrivé avant une erreur est gardé.
			if serr := d.Projects.SaveTranslations(c.Request.Context(), c.Param("id"), lang, ts); serr != nil {
				projects.Fail(c, serr)
				return
			}
			if err != nil && len(ts) == 0 {
				status, h := genError(err)
				c.JSON(status, h)
				return
			}
			slices.Sort(failed)
		}
		c.JSON(http.StatusOK, gin.H{"translations": out, "failed": failed, "remaining": remaining})
	})

	// Aperçu : le HTML traduit de chaque bloc.
	api.GET("/projects/:id/i18n/:lang/preview", func(c *gin.Context) {
		p, _, lang, ok := target(c)
		if !ok {
			return
		}
		lp, missing := p.Localized(lang)
		blocks := map[string]string{}
		for _, b := range lp.Blocks {
			blocks[b.ID] = b.HTML
		}
		c.JSON(http.StatusOK, gin.H{"blocks": blocks, "missing": len(missing)})
	})
}

// loadTranslations : la mémoire de traduction du projet, au format de l'export.
func loadTranslations(c *gin.Context, d Deps, p *export.Page) error {
	all, err := d.Projects.Translations(c.Request.Context(), c.Param("id"))
	if err != nil {
		return err
	}
	p.Translations = map[string]map[string]string{}
	for lang, m := range all {
		p.Translations[lang] = make(map[string]string, len(m))
		for h, t := range m {
			p.Translations[lang][h] = t.Text
		}
	}
	return nil
}
