package export

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"nadaprod/pkg/i18n"
)

func trPage() Page {
	tr := func(pairs ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i < len(pairs); i += 2 {
			m[i18n.Key(pairs[i])] = pairs[i+1]
		}
		return m
	}
	return Page{
		Name: "Démo",
		SEO:  SEO{Lang: "fr", Langs: []string{"en", "es", "fr", "en", "EN", "bad lang"}, Description: "Une page de démo"},
		Blocks: []Block{
			{ID: "h", Type: "hero", HTML: `<section><h1>Bonjour</h1><p>Lisez <a href="#">ceci</a></p><img src="https://placehold.co/10x10" alt="Une photo"></section>`},
		},
		Translations: map[string]map[string]string{
			"en": tr("Bonjour", "Hello", "Lisez <1>ceci</1>", "Read <1>this</1>", "Une photo", "A photo", "Une page de démo", "A demo page"),
			"es": tr("Bonjour", "Hola"),
		},
	}
}

func TestLocalized(t *testing.T) {
	p := trPage()
	en, miss := p.Localized("en")
	h := en.Blocks[0].HTML
	for _, want := range []string{"<h1>Hello</h1>", `Read <a href="#">this</a>`, `alt="A photo"`} {
		if !strings.Contains(h, want) {
			t.Errorf("manque %s dans %s", want, h)
		}
	}
	if en.SEO.Lang != "en" || en.SEO.Description != "A demo page" || len(miss) != 0 {
		t.Errorf("SEO %+v, manquants %v", en.SEO, miss)
	}
	if p.Blocks[0].HTML == h {
		t.Error("la page d'origine ne doit pas être modifiée")
	}

	es, miss := p.Localized("es")
	if !strings.Contains(es.Blocks[0].HTML, "<h1>Hola</h1>") || strings.Join(miss, "|") != "Lisez <1>ceci</1>|Une photo|Une page de démo" {
		t.Errorf("es : %v", miss)
	}

	same, miss := p.Localized("fr")
	if same.Blocks[0].HTML != p.Blocks[0].HTML || len(miss) != 0 {
		t.Error("langue source : page inchangée")
	}
}

func TestPlainSegment(t *testing.T) {
	seg, ok := PlainSegment("R&D — l'équipe")
	if !ok || seg.Src != "R&amp;D — l'équipe" || seg.Hash != i18n.Key(seg.Src) {
		t.Fatalf("%+v", seg)
	}
	if _, ok := PlainSegment("2026"); ok {
		t.Error("sans lettre : pas de segment")
	}
}

func siteFiles(t *testing.T, p Page, compiles *int) (map[string]string, Result) {
	t.Helper()
	hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: readCloser{bytes.NewReader([]byte("PNG"))}}, nil
	})}
	comp := func(page []byte, cfg string) ([]byte, error) { *compiles++; return []byte(".c{}"), nil }
	res, err := Site(context.Background(), p, SiteOptions{Compiler: comp, HTTP: hc})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range res.Files {
		got[f.Path] = string(f.Data)
	}
	return got, res
}

func TestSiteLanguages(t *testing.T) {
	compiles := 0
	got, res := siteFiles(t, trPage(), &compiles)
	if compiles != 1 {
		t.Errorf("CSS compilé %d fois, attendu 1", compiles)
	}
	for _, f := range []string{"index.html", "en/index.html", "es/index.html", "styles.css", "favicon.svg", "assets/image-01.png"} {
		if _, ok := got[f]; !ok {
			t.Errorf("manque %s (%v)", f, keys(got))
		}
	}
	if len(res.Files) != 6 {
		t.Errorf("fichiers : %v (langues normalisées : en, es)", keys(got))
	}

	root, en := got["index.html"], got["en/index.html"]
	for _, want := range []string{`<html lang="en">`, `href="../styles.css"`, `href="../favicon.svg"`, `src="../assets/image-01.png"`,
		"<h1>Hello</h1>", `<meta name="description" content="A demo page">`,
		`<link rel="alternate" hreflang="fr" href="../">`, `<link rel="alternate" hreflang="es" href="../es/">`,
		`<link rel="alternate" hreflang="x-default" href="../">`, `aria-current="page" style="color:#111`, `>EN</a>`} {
		if !strings.Contains(en, want) {
			t.Errorf("en/index.html : manque %s", want)
		}
	}
	for _, want := range []string{`<html lang="fr">`, `href="styles.css"`, `src="assets/image-01.png"`, "<h1>Bonjour</h1>",
		`hreflang="fr" href="./"`, `hreflang="en" href="en/"`, `<nav aria-label="Langue"`} {
		if !strings.Contains(root, want) {
			t.Errorf("index.html : manque %s", want)
		}
	}
	if strings.Contains(en, "canonical") {
		t.Error("pas de canonical sans URL connue")
	}
	if strings.Join(res.Warnings, "|") != "es : 3 texte(s) non traduit(s) — source conservée" {
		t.Errorf("avertissements : %v", res.Warnings)
	}
}

func TestSiteLanguagesAbsolute(t *testing.T) {
	p := trPage()
	p.SEO.URL = "https://demo.example/index.html"
	compiles := 0
	got, _ := siteFiles(t, p, &compiles)
	en := got["en/index.html"]
	for _, want := range []string{`<link rel="canonical" href="https://demo.example/en/">`, `<meta property="og:url" content="https://demo.example/en/">`,
		`hreflang="fr" href="https://demo.example/index.html"`, `hreflang="es" href="https://demo.example/es/"`} {
		if !strings.Contains(en, want) {
			t.Errorf("manque %s", want)
		}
	}
	if !strings.Contains(got["index.html"], `<link rel="canonical" href="https://demo.example/index.html">`) {
		t.Error("canonical de la source inchangé")
	}
}

func TestSiteSingleLanguage(t *testing.T) {
	p := trPage()
	p.SEO.Langs = []string{"fr"} // = la source : aucune langue cible
	compiles := 0
	got, _ := siteFiles(t, p, &compiles)
	if strings.Contains(got["index.html"], "hreflang") || strings.Contains(got["index.html"], "<nav aria-label") || len(got) != 4 {
		t.Errorf("une seule langue : ni sélecteur, ni hreflang, ni dossier (%v)", keys(got))
	}
}
