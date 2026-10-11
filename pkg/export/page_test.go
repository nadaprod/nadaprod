package export

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var strictCfgRe = regexp.MustCompile(`(?s)^\s*tailwind\.config\s*=\s*\{.*\}\s*;?\s*$`)

func demoTheme() Theme {
	return Theme{
		Enabled: true, Mode: "light", Primary: "#7C3AED", FontDisplay: "Space Grotesk", FontSans: "Inter", Radius: "xl",
		Scales: map[string]map[string]string{
			"primary": {"50": "#F5F3FF", "600": "#7C3AED", "950": "#2E1065"},
			"accent":  {"500": "#0EA5E9"},
		},
	}
}

func TestTailwindConfig(t *testing.T) {
	if cfg := TailwindConfig(Theme{}); cfg != "" {
		t.Errorf("sans design system, pas de config : %q", cfg)
	}
	cfg := TailwindConfig(demoTheme())
	if !strictCfgRe.MatchString(cfg) {
		t.Errorf("la config doit rester « stricte » pour le pipeline de build : %s", cfg)
	}
	for _, want := range []string{`"600":"#7c3aed"`, `"accent":{"500":"#0ea5e9"}`, `"display":["\"Space Grotesk\""`, `"brand":"1.25rem"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("il manque %s dans %s", want, cfg)
		}
	}

	// La config est exécutée par le binaire Tailwind : rien d'invalide ne passe.
	evil := demoTheme()
	evil.FontDisplay = `x"]}}};require("child_process").exec("id");//`
	evil.FontSans = "Inter'; DROP"
	evil.Radius = "}; process.exit(1); //"
	evil.Scales["primary"]["600"] = `#fff"};process.exit(1);//`
	evil.Scales["primary"]["__proto__"] = "#ffffff"
	evil.Scales["accent"] = map[string]string{"500": "red"}
	cfg = TailwindConfig(evil)
	for _, bad := range []string{"require", "process", "DROP", "__proto__", "red", "accent"} {
		if strings.Contains(cfg, bad) {
			t.Errorf("valeur non validée dans la config (%s) : %s", bad, cfg)
		}
	}
	if !strings.Contains(cfg, `"brand":"0.75rem"`) {
		t.Errorf("rayon inconnu ⇒ valeur par défaut : %s", cfg)
	}
}

func demoPage() Page {
	return Page{
		Name:  "Projet test",
		Brand: Brand{Name: "Nébula", Tone: "SaaS B2B, moderne"},
		Theme: demoTheme(),
		Blocks: []Block{
			{ID: "a", Type: "header", HTML: `<header class="bg-primary-600 font-display">Nébula</header>`},
			{ID: "b", Type: "hero", HTML: `<section><h1 class="text-5xl">Le CRM <span>simple</span> &amp; rapide</h1><p class="mt-4">Pilotez vos ventes sans tableur.</p><img src="https://placehold.co/800x500?text=A&amp;x=1" data-photo="team" alt=""><img src="https://evil.example/x.png" alt=""></section>`},
			{ID: "c", Type: "cta", HTML: "   "},
			{ID: "d", Type: "hero", HTML: `<section><h2>Encore</h2></section>`},
		},
	}
}

func fakeCompiler(page []byte, cfg string) ([]byte, error) {
	if !bytes.Contains(page, []byte("cdn.tailwindcss.com")) || !strings.HasPrefix(cfg, "tailwind.config = ") {
		return nil, errors.New("le compilateur doit recevoir le document CDN et la config du thème")
	}
	return []byte(".compiled{color:red}"), nil
}

func TestHTMLExport(t *testing.T) {
	res := HTML(demoPage(), fakeCompiler)
	if !res.Compiled || len(res.Files) != 1 || res.Files[0].Path != "index.html" || len(res.Warnings) != 0 {
		t.Fatalf("résultat : %+v", res)
	}
	doc := string(res.Files[0].Data)
	for _, want := range []string{
		`<html lang="fr">`,
		`<title>Nébula — Le CRM simple &amp; rapide</title>`,
		`<meta name="description" content="Pilotez vos ventes sans tableur.">`,
		`<meta property="og:title" content="Nébula — Le CRM simple &amp; rapide">`,
		`<meta property="og:site_name" content="Nébula">`,
		`<meta name="twitter:card" content="summary">`,
		`<meta name="theme-color" content="#7C3AED">`,
		`<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,`,
		`family=Space+Grotesk:wght@400;500;600;700&amp;family=Inter:wght@`,
		`<style>.compiled{color:red}</style>`,
		`<body class="antialiased font-sans">`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("il manque %q", want)
		}
	}
	if strings.Contains(doc, "cdn.tailwindcss.com") || strings.Contains(doc, "tailwind.config") {
		t.Error("un export compilé ne doit plus rien devoir au CDN")
	}
	if strings.Count(doc, "<section>") != 2 {
		t.Error("les blocs vides sont ignorés, les autres conservés")
	}

	// SEO explicite, URL invalides écartées.
	p := demoPage()
	p.SEO = SEO{Title: strings.Repeat("Long ", 30), Description: "Desc", OGImage: "https://cdn.example/og.png", URL: "javascript:alert(1)", Lang: "en-US"}
	doc = string(HTML(p, fakeCompiler).Files[0].Data)
	if !strings.Contains(doc, `<html lang="en-US">`) || !strings.Contains(doc, `summary_large_image`) || strings.Contains(doc, "javascript:") || strings.Contains(doc, "canonical") {
		t.Errorf("SEO explicite :\n%s", doc[:600])
	}
	if m := regexp.MustCompile(`<title>(.*?)</title>`).FindStringSubmatch(doc); len([]rune(m[1])) > 70 {
		t.Errorf("titre non borné : %d", len([]rune(m[1])))
	}

	// Compilation impossible ⇒ repli CDN + config du thème, avec avertissement.
	res = HTML(demoPage(), func([]byte, string) ([]byte, error) { return nil, errors.New("binaire absent") })
	doc = string(res.Files[0].Data)
	if res.Compiled || len(res.Warnings) != 1 || !strings.Contains(doc, "cdn.tailwindcss.com") || !strings.Contains(doc, "<script>tailwind.config = {") {
		t.Errorf("repli CDN : %+v", res.Warnings)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSiteExport(t *testing.T) {
	fonts := t.TempDir()
	os.MkdirAll(filepath.Join(fonts, "Inter"), 0o755)
	os.WriteFile(filepath.Join(fonts, "Inter", "regular.ttf"), []byte("TTF-400"), 0o644)
	os.WriteFile(filepath.Join(fonts, "Inter", "700.ttf"), []byte("TTF-700"), 0o644)

	var fetched []string
	hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		fetched = append(fetched, r.URL.String())
		rec := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: http.NoBody}
		rec.Body = readCloser{bytes.NewReader([]byte("PNG"))}
		return rec, nil
	})}

	res, err := Site(context.Background(), demoPage(), SiteOptions{Compiler: fakeCompiler, FontsDir: fonts, HTTP: hc, Components: "jsx"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range res.Files {
		got[f.Path] = string(f.Data)
	}
	for _, want := range []string{"index.html", "styles.css", "favicon.svg", "assets/image-01.png", "assets/fonts/Inter/regular.ttf", "assets/fonts/Inter/700.ttf",
		"components/Header.jsx", "components/Hero.jsx", "components/Hero2.jsx", "components/Page.jsx"} {
		if _, ok := got[want]; !ok {
			t.Errorf("fichier manquant : %s (présents : %v)", want, keys(got))
		}
	}
	if len(fetched) != 1 || fetched[0] != "https://placehold.co/800x500?text=A&x=1" {
		t.Errorf("seuls les hôtes connus sont rapatriés, URL désentitisée : %v", fetched)
	}
	idx := got["index.html"]
	if !strings.Contains(idx, `src="assets/image-01.png"`) || !strings.Contains(idx, `src="https://evil.example/x.png"`) {
		t.Error("réécriture des src : hôte connu → local, hôte inconnu → intact")
	}
	if !strings.Contains(idx, `href="styles.css"`) || !strings.Contains(idx, `href="favicon.svg"`) || strings.Contains(idx, "<style>") {
		t.Error("le site référence styles.css et favicon.svg")
	}
	css := got["styles.css"]
	if !strings.HasPrefix(css, "@font-face{font-family:'Inter'") || !strings.Contains(css, "assets/fonts/Inter/700.ttf") || !strings.HasSuffix(css, ".compiled{color:red}") {
		t.Errorf("styles.css : %s", css)
	}
	// Space Grotesk absente du miroir ⇒ lien Google conservé + avertissement.
	if !strings.Contains(idx, "fonts.googleapis.com") || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Space Grotesk") {
		t.Errorf("polices manquantes : %v", res.Warnings)
	}
	if !strings.Contains(got["components/Page.jsx"], "import Hero2 from './Hero2.jsx';") || !strings.Contains(got["components/Hero.jsx"], `src="assets/image-01.png"`) {
		t.Errorf("composants :\n%s", got["components/Page.jsx"])
	}

	// Archive : tout y est, dans l'ordre.
	raw, err := Zip(res.Files)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(zr.File) != len(res.Files) {
		t.Fatalf("zip : %v (%d fichiers)", err, len(zr.File))
	}

	// Vue + page vide.
	res, _ = Site(context.Background(), demoPage(), SiteOptions{Compiler: fakeCompiler, HTTP: hc, Components: "vue"})
	found := false
	for _, f := range res.Files {
		if f.Path == "components/Page.vue" && strings.Contains(string(f.Data), "import Header from './Header.vue';") {
			found = true
		}
	}
	if !found {
		t.Error("components/Page.vue attendu")
	}
	if _, err := Site(context.Background(), Page{Blocks: []Block{{HTML: " "}}}, SiteOptions{}); err == nil {
		t.Error("une page sans bloc généré ne s'exporte pas")
	}
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }

func keys(m map[string]string) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Nébula Café !":  "nebula-cafe",
		"  --Ça déjà-- ": "ca-deja",
		"":               "page",
		"日本":             "page",
		"A  B___C":       "a-b-c",
		"élan à l'œuvre": "elan-a-l-uvre",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, attendu %q", in, got, want)
		}
	}
}
