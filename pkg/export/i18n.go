package export

import (
	"cmp"
	"html"
	"slices"
	"strings"

	"nadaprod/pkg/i18n"
)

// ---------- Une URL par langue (voir doc/I18N.md) ----------

const maxLangs = 12

// targetLangs : langues cibles valides, distinctes, hors langue source.
func targetLangs(source string, langs []string) []string {
	out := make([]string, 0, len(langs))
	for _, l := range langs {
		if i18n.LangRe.MatchString(l) && !strings.EqualFold(l, source) && !slices.Contains(out, l) && len(out) < maxLangs {
			out = append(out, l)
		}
	}
	return out
}

func dedupe(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func (p Page) sourceLang() string {
	if langRe.MatchString(p.SEO.Lang) {
		return p.SEO.Lang
	}
	return "fr"
}

// Languages : langue source et langues cibles valides de la page.
func (p Page) Languages() (source string, targets []string) {
	source = p.sourceLang()
	return source, targetLangs(source, p.SEO.Langs)
}

// PlainSegment : le segment d'un texte brut (titre, description SEO) — même
// forme que ceux d'i18n.Extract, donc même clé de traduction.
func PlainSegment(text string) (i18n.Segment, bool) {
	segs := i18n.Extract("<p>" + html.EscapeString(text) + "</p>")
	if len(segs) == 0 {
		return i18n.Segment{}, false
	}
	return segs[0], true
}

// apply : un fragment traduit dans lang, et ses sources sans traduction.
func (p Page) apply(lang, fragment string) (string, []string) {
	dict := p.Translations[lang]
	return i18n.Apply(fragment, func(src string) (string, bool) {
		t, ok := dict[i18n.Key(src)]
		return t, ok
	})
}

// Localized : la page dans lang — blocs, titre et description traduits,
// SEO.Lang = lang. Une source sans traduction reste en place et figure dans
// la liste renvoyée (sans doublon). lang = langue source ⇒ p inchangée.
func (p Page) Localized(lang string) (Page, []string) {
	if strings.EqualFold(lang, p.sourceLang()) {
		return p, []string{}
	}
	out := p
	out.Blocks = make([]Block, len(p.Blocks))
	var missing []string
	for i, b := range p.Blocks {
		var miss []string
		b.HTML, miss = p.apply(lang, b.HTML)
		out.Blocks[i] = b
		missing = append(missing, miss...)
	}
	for _, f := range []*string{&out.SEO.Title, &out.SEO.Description} {
		seg, ok := PlainSegment(*f)
		if !ok {
			continue
		}
		if t, ok := p.Translations[lang][seg.Hash]; ok && i18n.Valid(seg.Src, t) {
			*f = html.UnescapeString(t)
		} else {
			missing = append(missing, seg.Src)
		}
	}
	out.SEO.Lang = lang
	return out, dedupe(missing)
}

// subdirAssets : les images rapatriées (chemins de local) vues depuis <lang>/.
func subdirAssets(body string, local map[string]bool) string {
	return imgSrcRe.ReplaceAllStringFunc(body, func(m string) string {
		sub := imgSrcRe.FindStringSubmatch(m)
		if v := html.UnescapeString(sub[2][1 : len(sub[2])-1]); local[v] {
			return sub[1] + `"../` + v + `"`
		}
		return m
	})
}

// langBase : URL du dossier racine du site (avec /), "" si l'URL est inconnue.
func (p Page) langBase() string {
	u := p.SEO.URL
	if u == "" {
		return ""
	}
	if i := strings.LastIndex(u, "/"); i > len("https://") && strings.Contains(u[i:], ".") {
		return u[:i+1] // …/index.html → …/
	}
	return strings.TrimSuffix(u, "/") + "/"
}

// langURL : URL absolue de la page dans lang ("" si l'URL du site est inconnue).
func (p Page) langURL(lang string) string {
	if lang == p.SEO.Lang {
		return p.SEO.URL
	}
	if base := p.langBase(); base != "" {
		return base + lang + "/"
	}
	return ""
}

// href : lien vers la page dans to, depuis la page dans from — absolu si
// l'URL du site est connue, relatif sinon.
func (p Page) href(from, to string) string {
	if u := p.langURL(to); u != "" {
		return u
	}
	up := ""
	if from != p.SEO.Lang {
		up = "../"
	}
	if to == p.SEO.Lang {
		return cmp.Or(up, "./")
	}
	return up + to + "/"
}

func (p Page) allLangs() []string { return append([]string{p.SEO.Lang}, p.SEO.Langs...) }

// alternates : <link hreflang> de toutes les langues + x-default (la source).
func (p Page) alternates(from string) string {
	if len(p.SEO.Langs) == 0 {
		return ""
	}
	var b strings.Builder
	link := func(lang, href string) {
		b.WriteString(`<link rel="alternate" hreflang="` + html.EscapeString(lang) + `" href="` + html.EscapeString(href) + "\">\n")
	}
	for _, l := range p.allLangs() {
		link(l, p.href(from, l))
	}
	link("x-default", p.href(from, p.SEO.Lang))
	return b.String()
}

// Libellés natifs du sélecteur (autonymes) ; code en capitales sinon.
var autonyms = map[string]string{
	"fr": "Français", "en": "English", "es": "Español", "it": "Italiano", "pt": "Português", "de": "Deutsch",
	"nl": "Nederlands", "pl": "Polski", "sv": "Svenska", "da": "Dansk", "no": "Norsk", "fi": "Suomi",
	"cs": "Čeština", "ro": "Română", "el": "Ελληνικά", "tr": "Türkçe", "ru": "Русский", "uk": "Українська",
	"ar": "العربية", "he": "עברית", "ja": "日本語", "ko": "한국어", "zh": "中文", "hi": "हिन्दी",
}

var navLabel = map[string]string{"fr": "Langue", "es": "Idioma", "pt": "Idioma", "it": "Lingua", "de": "Sprache", "nl": "Taal"}

// switcher : petit sélecteur de langue, styles inline (le CSS compilé est
// purgé : aucune classe nouvelle), lisible sur fond clair comme sombre.
func (p Page) switcher(current string) string {
	if len(p.SEO.Langs) == 0 {
		return ""
	}
	label := navLabel[strings.SplitN(current, "-", 2)[0]]
	if label == "" {
		label = "Language"
	}
	var b strings.Builder
	b.WriteString(`<nav aria-label="` + label + `" style="position:fixed;right:12px;bottom:12px;z-index:2147483000;display:flex;gap:2px;padding:3px;border-radius:999px;background:rgba(17,17,17,.78);-webkit-backdrop-filter:blur(6px);backdrop-filter:blur(6px);box-shadow:0 1px 6px rgba(0,0,0,.3);font:600 11px/1 system-ui,-apple-system,sans-serif;letter-spacing:.05em">`)
	for _, l := range p.allLangs() {
		e := html.EscapeString(l)
		style := "color:#fff;text-decoration:none;padding:6px 9px;border-radius:999px;opacity:.75"
		cur := ""
		if l == current {
			style = "color:#111;background:#fff;text-decoration:none;padding:6px 9px;border-radius:999px"
			cur = ` aria-current="page"`
		}
		title := autonyms[strings.SplitN(l, "-", 2)[0]]
		b.WriteString(`<a href="` + html.EscapeString(p.href(current, l)) + `" hreflang="` + e + `" lang="` + e + `" title="` + html.EscapeString(title) + `"` + cur + ` style="` + style + `">` + strings.ToUpper(e) + `</a>`)
	}
	b.WriteString("</nav>\n")
	return b.String()
}
