package studio

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"nadaprod/pkg/gemini"
	"nadaprod/pkg/projects"
)

// fakeTranslator : « traduit » en majuscules, sauf les sources listées dans
// broken (marqueurs perdus) ; mémorise les requêtes.
type fakeTranslator struct {
	mu     sync.Mutex
	reqs   []gemini.TranslateRequest
	broken map[string]bool
}

func (f *fakeTranslator) Translate(_ context.Context, req gemini.TranslateRequest) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	out := map[string]string{}
	for id, src := range req.Items {
		if f.broken[src] {
			out[id] = "BROKEN <9/>" // marqueur inventé ⇒ rejeté
		} else {
			out[id] = strings.ToUpper(src)
		}
	}
	return out, nil
}

type i18nState struct {
	Source   string   `json:"source"`
	Langs    []string `json:"langs"`
	Segments []struct {
		Hash, Src, Attr string
		Blocks          []string
	} `json:"segments"`
	Translations map[string]map[string]struct{ Text, Status string } `json:"translations"`
}

func (e *env) state(id string) i18nState {
	e.t.Helper()
	w := e.do("GET", "/api/projects/"+id+"/i18n", nil)
	if w.Code != 200 {
		e.t.Fatalf("GET i18n : %d %s", w.Code, w.Body)
	}
	var s i18nState
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) hashOf(s i18nState, src string) string {
	for _, seg := range s.Segments {
		if seg.Src == src {
			return seg.Hash
		}
	}
	e.t.Fatalf("segment absent : %q", src)
	return ""
}

func TestI18nFlow(t *testing.T) {
	tr := &fakeTranslator{broken: map[string]bool{"Cassé": true}}
	sites := &fakeSites{pages: map[string][]byte{}}
	e := newEnv(t, Deps{Translator: tr, Sites: sites, SiteAccess: func(string, string) bool { return true },
		Compile: func(page []byte, cfg string) ([]byte, error) { return []byte(".x{}"), nil }})
	id := e.project(
		projects.BlockInput{ID: "a", Type: "hero", HTML: `<section><h1>Bonjour</h1><p>Lisez <a href="/cgu">nos conditions</a>.</p><img src="https://placehold.co/1x1" alt="Une image"></section>`},
		projects.BlockInput{ID: "b", Type: "cta", HTML: `<section><p>Bonjour</p><p>Cassé</p></section>`},
	)
	seo := json.RawMessage(`{"title":"Accueil","lang":"fr","langs":["en","xx_bad","en"]}`)
	if w := e.do("PUT", "/api/projects/"+id, projects.MetaInput{SEO: &seo}); w.Code != 200 {
		t.Fatalf("SEO : %d %s", w.Code, w.Body)
	}

	s := e.state(id)
	if s.Source != "fr" || strings.Join(s.Langs, ",") != "en" {
		t.Fatalf("langues : %s %v", s.Source, s.Langs)
	}
	var got []string
	for _, seg := range s.Segments {
		got = append(got, seg.Src+"@"+strings.Join(seg.Blocks, "+"))
	}
	if strings.Join(got, " | ") != "Accueil@seo | Bonjour@a+b | Lisez <1>nos conditions</1>.@a | Une image@a | Cassé@b" {
		t.Fatalf("segments : %v", got)
	}

	// Langue hors projet, segment inconnu, marqueurs altérés.
	if w := e.do("PUT", "/api/projects/"+id+"/i18n/de", gin.H{"items": []gin.H{}}); w.Code != 400 {
		t.Errorf("langue hors projet : %d", w.Code)
	}
	if w := e.do("PUT", "/api/projects/"+id+"/i18n/en", gin.H{"items": []gin.H{{"hash": "0000000000000000", "text": "x"}}}); w.Code != 400 {
		t.Errorf("segment inconnu : %d", w.Code)
	}
	lisez := e.hashOf(s, "Lisez <1>nos conditions</1>.")
	w := e.do("PUT", "/api/projects/"+id+"/i18n/en", gin.H{"items": []gin.H{{"hash": lisez, "text": "Read our terms."}}})
	if w.Code != 422 || decode(t, w)["code"] != "invalid_markers" {
		t.Errorf("marqueurs : %d %s", w.Code, w.Body)
	}

	// Saisie humaine, puis traduction automatique du reste : l'humain n'est pas écrasé.
	if w := e.do("PUT", "/api/projects/"+id+"/i18n/en", gin.H{"items": []gin.H{{"hash": lisez, "text": "Read <1>our terms</1>."}}}); w.Code != 200 {
		t.Fatalf("saisie : %d %s", w.Code, w.Body)
	}
	w = e.do("POST", "/api/projects/"+id+"/i18n/en/translate", gin.H{})
	res := decode(t, w)
	if w.Code != 200 || len(res["translations"].(map[string]any)) != 3 || len(res["failed"].([]any)) != 1 || res["remaining"].(float64) != 0 {
		t.Fatalf("traduction : %d %s", w.Code, w.Body)
	}
	if n := len(tr.reqs[0].Items); n != 4 || tr.reqs[0].From != "fr" || tr.reqs[0].To != "en" || tr.reqs[0].Brand.Name != "Nébula" {
		t.Fatalf("requête traducteur : %+v", tr.reqs[0])
	}
	s = e.state(id)
	if en := s.Translations["en"]; en[lisez].Status != "human" || en[e.hashOf(s, "Bonjour")].Text != "BONJOUR" || en[e.hashOf(s, "Bonjour")].Status != "machine" {
		t.Fatalf("état : %+v", en)
	}

	// Aperçu traduit : le texte manquant garde la source.
	w = e.do("GET", "/api/projects/"+id+"/i18n/en/preview", nil)
	prev := decode(t, w)
	blocks := prev["blocks"].(map[string]any)
	if !strings.Contains(blocks["a"].(string), `Read <a href="/cgu">our terms</a>.`) || !strings.Contains(blocks["a"].(string), `alt="UNE IMAGE"`) ||
		!strings.Contains(blocks["b"].(string), "<p>Cassé</p>") || prev["missing"].(float64) != 1 {
		t.Fatalf("aperçu : %s", w.Body)
	}

	// Régénérer un bloc ne casse rien : le texte inchangé garde sa traduction.
	if w := e.do("PUT", "/api/projects/"+id+"/blocks/b", gin.H{"type": "cta", "html": `<section class="new"><h2>Bonjour</h2><p>Nouveau</p></section>`}); w.Code != 200 {
		t.Fatalf("régénération : %d %s", w.Code, w.Body)
	}
	s = e.state(id)
	if en := s.Translations["en"]; en[e.hashOf(s, "Bonjour")].Text != "BONJOUR" {
		t.Fatal("traduction perdue à la régénération")
	}
	if _, ok := s.Translations["en"][e.hashOf(s, "Nouveau")]; ok {
		t.Fatal("texte nouveau déjà traduit ?")
	}

	// Export HTML dans une langue ; ZIP : une URL par langue.
	w = e.do("POST", "/api/projects/"+id+"/export", gin.H{"format": "html", "lang": "en"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<html lang="en">`) || !strings.Contains(w.Body.String(), "<title>ACCUEIL</title>") {
		t.Fatalf("export html en : %d %s", w.Code, w.Body.String()[:min(400, w.Body.Len())])
	}
	if w := e.do("POST", "/api/projects/"+id+"/export", gin.H{"format": "html", "lang": "de"}); w.Code != 400 {
		t.Errorf("export langue absente : %d", w.Code)
	}
	w = e.do("POST", "/api/projects/"+id+"/export", gin.H{"format": "zip"})
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		files[f.Name] = string(b)
	}
	if !strings.Contains(files["en/index.html"], `href="../styles.css"`) || !strings.Contains(files["index.html"], `hreflang="en"`) {
		t.Fatalf("zip multilingue : %v", files["en/index.html"])
	}

	// Passerelle site : la page et sa copie par langue.
	w = e.do("POST", "/api/projects/"+id+"/site", gin.H{"site": "demo", "path": "index.html"})
	if w.Code != 200 || !strings.Contains(string(sites.pages["demo/en/index.html"]), "Read <a") || !strings.Contains(string(sites.pages["demo/index.html"]), "Lisez") {
		t.Fatalf("passerelle : %d %s %v", w.Code, w.Body, len(sites.pages))
	}

	// Sans traducteur : 503 no_translator. Propriété : un autre compte ne voit rien.
	e2 := newEnv(t, Deps{})
	id2 := e2.project(projects.BlockInput{ID: "a", Type: "hero", HTML: "<h1>Salut</h1>"})
	seo2 := json.RawMessage(`{"langs":["en"]}`)
	e2.do("PUT", "/api/projects/"+id2, projects.MetaInput{SEO: &seo2})
	if w := e2.do("POST", "/api/projects/"+id2+"/i18n/en/translate", nil); w.Code != 503 || decode(t, w)["code"] != "no_translator" {
		t.Errorf("sans traducteur : %d %s", w.Code, w.Body)
	}
	e.user = "intrus@x.fr"
	for _, path := range []string{"/i18n", "/i18n/en/preview"} {
		if w := e.do("GET", "/api/projects/"+id+path, nil); w.Code != 404 {
			t.Errorf("%s par un autre compte : %d", path, w.Code)
		}
	}
}
