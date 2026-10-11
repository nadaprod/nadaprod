package studio

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nadaprod/pkg/gemini"
	"nadaprod/pkg/projects"
	"nadaprod/pkg/prompts"
)

// fakeGen : générateur de test — renvoie ce qu'on lui dit, note ce qu'il reçoit.
type fakeGen struct {
	html string
	err  error
	got  []gemini.GenerateRequest
}

func (f *fakeGen) Model() string { return "fake-flash" }
func (f *fakeGen) GenerateBlock(_ context.Context, _ prompts.BlockSpec, req gemini.GenerateRequest) (string, error) {
	f.got = append(f.got, req)
	return f.html, f.err
}
func (f *fakeGen) StreamBlock(ctx context.Context, spec prompts.BlockSpec, req gemini.GenerateRequest, onDelta func(string)) (string, error) {
	if f.err == nil {
		onDelta("<section>")
		onDelta("ok</section>")
	}
	return f.GenerateBlock(ctx, spec, req)
}

type env struct {
	t     *testing.T
	r     *gin.Engine
	gen   *fakeGen
	store *projects.Store
	user  string
}

func newEnv(t *testing.T, d Deps) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := projects.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e := &env{t: t, gen: &fakeGen{html: "<section>ok</section>"}, store: store, user: "a@x.fr"}
	d.Gen, d.Projects = e.gen, store
	e.r = gin.New()
	api := e.r.Group("/api", func(c *gin.Context) { c.Set("user", e.user); c.Set("isRoot", false) }) // tient lieu de RequireAuth
	store.RegisterRoutes(api)
	Register(api, d)
	return e
}

func (e *env) do(method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("réponse non JSON (%d) : %s", w.Code, w.Body.String())
	}
	return m
}

func TestGenerate(t *testing.T) {
	e := newEnv(t, Deps{})
	w := e.do("POST", "/api/generate", gin.H{"block": "hero", "prompt": "sombre", "variant": 2, "theme": gin.H{"enabled": true, "radius": "xl"}})
	m := decode(t, w)
	if w.Code != 200 || m["html"] != "<section>ok</section>" || m["model"] != "fake-flash" || m["block"] != "hero" {
		t.Fatalf("%d %v", w.Code, m)
	}
	if got := e.gen.got[0]; got.Variant != 2 || got.Theme == nil || got.Theme.Radius != "xl" || got.Prompt != "sombre" {
		t.Fatalf("requête transmise au générateur : %+v", got)
	}

	if w := e.do("POST", "/api/generate", gin.H{"block": "inconnu"}); w.Code != 400 || decode(t, w)["code"] != "invalid" {
		t.Errorf("bloc inconnu : %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/generate", gin.H{}); w.Code != 400 {
		t.Errorf("bloc manquant : %d", w.Code)
	}

	// Les erreurs typées de Gemini gardent leur code et leur délai.
	e.gen.err = &gemini.Error{Code: "quota", Status: 429, Message: "gemini 429 : quota", RetryAfter: 12}
	w = e.do("POST", "/api/generate", gin.H{"block": "hero"})
	if m := decode(t, w); w.Code != 429 || m["code"] != "quota" || m["retry_after"] != float64(12) {
		t.Errorf("quota : %d %v", w.Code, m)
	}
	e.gen.err = context.DeadlineExceeded
	if w = e.do("POST", "/api/generate", gin.H{"block": "hero"}); w.Code != 504 || decode(t, w)["code"] != "timeout" {
		t.Errorf("délai : %d %s", w.Code, w.Body)
	}
	e.gen.err = errors.New("boum")
	if w = e.do("POST", "/api/generate", gin.H{"block": "hero"}); w.Code != 502 || decode(t, w)["code"] != "upstream" {
		t.Errorf("erreur quelconque : %d %s", w.Code, w.Body)
	}
}

func TestGenerateStream(t *testing.T) {
	e := newEnv(t, Deps{})
	w := e.do("POST", "/api/generate/stream", gin.H{"block": "hero"})
	body := w.Body.String()
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") || w.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("en-têtes : %v", w.Header())
	}
	if strings.Count(body, "event: delta") != 2 || !strings.Contains(body, `u003csection`) ||
		!strings.Contains(body, "event: done") || !strings.Contains(body, `"model":"fake-flash"`) {
		t.Errorf("flux : %s", body)
	}

	e.gen.err = &gemini.Error{Code: "blocked", Message: "refusé"}
	body = e.do("POST", "/api/generate/stream", gin.H{"block": "hero"}).Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, `"code":"blocked"`) || strings.Contains(body, "event: done") {
		t.Errorf("flux en erreur : %s", body)
	}

	// Une requête invalide échoue AVANT l'ouverture du flux : JSON classique.
	if w := e.do("POST", "/api/generate/stream", gin.H{"block": "nope"}); w.Code != 400 || decode(t, w)["code"] != "invalid" {
		t.Errorf("validation avant flux : %d %s", w.Code, w.Body)
	}
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t, Deps{RatePerMin: 2})
	for i := 0; i < 2; i++ {
		if w := e.do("POST", "/api/generate", gin.H{"block": "hero"}); w.Code != 200 {
			t.Fatalf("appel %d : %d", i, w.Code)
		}
	}
	w := e.do("POST", "/api/generate", gin.H{"block": "hero"})
	if m := decode(t, w); w.Code != 429 || m["code"] != "rate_limited" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("limite : %d %v", w.Code, m)
	}
	if len(e.gen.got) != 2 {
		t.Errorf("une requête limitée ne doit pas atteindre Gemini (%d appels)", len(e.gen.got))
	}
	e.user = "b@x.fr" // la limite est par compte
	if w := e.do("POST", "/api/generate", gin.H{"block": "hero"}); w.Code != 200 {
		t.Errorf("autre compte : %d", w.Code)
	}
}

func TestLimiterRefill(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(60, time.Minute) // 1 jeton par seconde
	l.now = func() time.Time { return now }
	for i := 0; i < 60; i++ {
		if l.take("k") != 0 {
			t.Fatalf("jeton %d refusé", i)
		}
	}
	if wait := l.take("k"); wait <= 0 || wait > time.Second {
		t.Fatalf("attente annoncée : %v", wait)
	}
	now = now.Add(2 * time.Second)
	if l.take("k") != 0 || l.take("k") != 0 || l.take("k") == 0 {
		t.Fatal("2 s ⇒ exactement 2 jetons")
	}
}

func TestConfigAndImages(t *testing.T) {
	e := newEnv(t, Deps{Compile: func([]byte, string) ([]byte, error) { return nil, nil }})
	m := decode(t, e.do("GET", "/api/studio/config", nil))
	if m["model"] != "fake-flash" || m["compile"] != true || m["user"] != "a@x.fr" || m["images"].(map[string]any)["enabled"] != false {
		t.Errorf("config : %v", m)
	}
	if w := e.do("GET", "/api/images/search?q=team", nil); w.Code != 503 || decode(t, w)["code"] != "disabled" {
		t.Errorf("banque d'images absente : %d %s", w.Code, w.Body)
	}
	var blocks []map[string]any
	json.Unmarshal(e.do("GET", "/api/blocks", nil).Body.Bytes(), &blocks)
	if len(blocks) != 15 {
		t.Errorf("catalogue : %d blocs", len(blocks))
	}
	for _, b := range blocks {
		for k := range b {
			if strings.Contains(strings.ToLower(k), "prompt") {
				t.Errorf("le prompt système ne doit jamais sortir (%s)", k)
			}
		}
	}
}

func (e *env) project(blocks ...projects.BlockInput) string {
	e.t.Helper()
	w := e.do("POST", "/api/projects", projects.ProjectInput{Name: "Nébula Café", Brand: json.RawMessage(`{"name":"Nébula"}`), Blocks: blocks})
	if w.Code != 201 {
		e.t.Fatalf("création : %d %s", w.Code, w.Body)
	}
	return decode(e.t, w)["id"].(string)
}

func TestExport(t *testing.T) {
	e := newEnv(t, Deps{Compile: func(page []byte, cfg string) ([]byte, error) { return []byte(".x{}"), nil }})
	id := e.project(projects.BlockInput{ID: "a", Type: "hero", HTML: "<section><h1>Bonjour</h1></section>"})

	w := e.do("POST", "/api/projects/"+id+"/export", gin.H{"format": "html"})
	if w.Code != 200 || w.Header().Get("X-Export-Compiled") != "true" || !strings.Contains(w.Header().Get("Content-Disposition"), `"nebula-cafe.html"`) ||
		!strings.Contains(w.Body.String(), "<style>.x{}</style>") || !strings.Contains(w.Body.String(), "<title>Nébula — Bonjour</title>") {
		t.Fatalf("export html : %d %v", w.Code, w.Header())
	}

	w = e.do("POST", "/api/projects/"+id+"/export", gin.H{"format": "vue"})
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if w.Code != 200 || err != nil {
		t.Fatalf("export zip : %d %v", w.Code, err)
	}
	names := []string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, " "); got != "components/Hero.vue components/Page.vue favicon.svg index.html styles.css" {
		t.Errorf("contenu du zip : %s", got)
	}

	if w := e.do("POST", "/api/projects/"+id+"/export", gin.H{"format": "exe"}); w.Code != 400 {
		t.Errorf("format inconnu : %d", w.Code)
	}
	empty := e.project()
	if w := e.do("POST", "/api/projects/"+empty+"/export", gin.H{"format": "zip"}); w.Code != 422 {
		t.Errorf("page vide : %d %s", w.Code, w.Body)
	}

	// Le projet d'un autre compte est invisible (404, pas 403).
	e.user = "intrus@x.fr"
	for _, path := range []string{"/export", "/publish"} {
		if w := e.do("POST", "/api/projects/"+id+path, gin.H{"format": "html", "token": "t", "provider": "netlify"}); w.Code != 404 {
			t.Errorf("%s par un autre compte : %d", path, w.Code)
		}
	}
	if w := e.do("GET", "/api/projects/"+id, nil); w.Code != 404 {
		t.Errorf("lecture par un autre compte : %d", w.Code)
	}
}

func TestPublishNetlify(t *testing.T) {
	var zipped []byte
	var auth string
	netlify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/sites":
			auth = r.Header.Get("Authorization")
			zipped, _ = io.ReadAll(r.Body)
			io.WriteString(w, `{"id":"site-1","name":"nebula","ssl_url":"https://nebula.netlify.app","admin_url":"https://app.netlify.com/sites/nebula","deploy_id":"dep-1"}`)
		case r.Method == "GET" && r.URL.Path == "/deploys/dep-1":
			io.WriteString(w, `{"id":"dep-1","state":"ready","ssl_url":"https://nebula.netlify.app"}`)
		default:
			http.Error(w, `{"message":"inattendu : `+r.Method+" "+r.URL.Path+`"}`, 404)
		}
	}))
	defer netlify.Close()

	e := newEnv(t, Deps{NetlifyURL: netlify.URL, Compile: func([]byte, string) ([]byte, error) { return []byte(".x{}"), nil }})
	id := e.project(projects.BlockInput{ID: "a", Type: "hero", HTML: "<section><h1>Bonjour</h1></section>"})

	if w := e.do("POST", "/api/projects/"+id+"/publish", gin.H{"provider": "netlify"}); w.Code != 400 {
		t.Errorf("jeton manquant : %d", w.Code)
	}
	if w := e.do("POST", "/api/projects/"+id+"/publish", gin.H{"provider": "ftp", "token": "t"}); w.Code != 400 {
		t.Errorf("fournisseur inconnu : %d", w.Code)
	}

	w := e.do("POST", "/api/projects/"+id+"/publish", gin.H{"provider": "netlify", "token": " secret-token "})
	m := decode(t, w)
	if w.Code != 200 || m["result"].(map[string]any)["url"] != "https://nebula.netlify.app" || auth != "Bearer secret-token" {
		t.Fatalf("publication : %d %v (auth=%q)", w.Code, m, auth)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil || len(zr.File) != 3 {
		t.Fatalf("archive envoyée : %v", err)
	}

	// La cible est mémorisée avec le projet — jamais le jeton.
	p, _ := e.store.Get(context.Background(), id)
	if !strings.Contains(string(p.Publish), `"site":"site-1"`) || !strings.Contains(string(p.Publish), `"provider":"netlify"`) || strings.Contains(string(p.Publish), "secret") {
		t.Errorf("méta de publication : %s", p.Publish)
	}
	if m["rev"] != float64(p.Rev) {
		t.Errorf("la réponse doit porter la nouvelle révision : %v vs %d", m["rev"], p.Rev)
	}
}

// fakeSites : passerelle mémoire vers les sites importés.
type fakeSites struct{ pages map[string][]byte }

func (f *fakeSites) ReadPage(id, rel string) ([]byte, error) {
	b, ok := f.pages[id+"/"+rel]
	if !ok {
		return nil, errors.New("page introuvable")
	}
	return b, nil
}
func (f *fakeSites) SavePage(id, rel string, html []byte, overwrite bool) (string, error) {
	if _, ok := f.pages[id+"/"+rel]; ok && !overwrite {
		return "", errors.New("cette page existe déjà")
	}
	f.pages[id+"/"+rel] = html
	return rel, nil
}

func TestSiteBridge(t *testing.T) {
	sites := &fakeSites{pages: map[string][]byte{
		"demo/index.html": []byte(`<!DOCTYPE html><html lang="en"><head><title>Démo</title><meta name="description" content="Une démo"></head>` +
			`<body><header class="p-4">Logo</header><section><h1>Bonjour</h1></section>texte nu<footer>©</footer><script>window.x=1</script></body></html>`),
	}}
	e := newEnv(t, Deps{Sites: sites, SiteAccess: func(user, id string) bool { return user == "a@x.fr" && id == "demo" }})

	// Page existante → projet : un bloc par enfant du body, typé par sa balise.
	w := e.do("POST", "/api/projects/from-page", gin.H{"site": "demo", "path": "index.html"})
	m := decode(t, w)
	if w.Code != 201 || m["blocks"] != float64(3) {
		t.Fatalf("import : %d %v", w.Code, m)
	}
	p, _ := e.store.Get(context.Background(), m["id"].(string))
	if p.Name != "Démo" || p.Blocks[0].Type != "header" || p.Blocks[1].Type != "section" || p.Blocks[2].Type != "footer" ||
		!strings.HasPrefix(p.Blocks[2].HTML, "texte nu") || !strings.Contains(p.Blocks[2].HTML, "window.x=1") ||
		!strings.Contains(string(p.SEO), `"lang":"en"`) || !strings.Contains(string(p.Publish), `"site":"demo"`) {
		t.Fatalf("projet importé : %+v %s %s", p.Blocks, p.SEO, p.Publish)
	}
	if w := e.do("POST", "/api/projects/from-page", gin.H{"site": "autre", "path": "index.html"}); w.Code != 403 {
		t.Errorf("site non attribué : %d", w.Code)
	}
	if w := e.do("POST", "/api/projects/from-page", gin.H{"site": "demo", "path": "nope.html"}); w.Code != 404 {
		t.Errorf("page absente : %d", w.Code)
	}

	// Projet → page de site : document CDN complet, refus d'écraser sans overwrite, cible mémorisée.
	id := e.project(projects.BlockInput{ID: "a", Type: "hero", HTML: "<section><h1>Titre</h1></section>"})
	w = e.do("POST", "/api/projects/"+id+"/site", gin.H{"site": "demo", "path": "index.html"})
	if w.Code != 409 {
		t.Fatalf("écrasement silencieux : %d %s", w.Code, w.Body)
	}
	w = e.do("POST", "/api/projects/"+id+"/site", gin.H{"site": "demo", "path": "nouvelle.html"})
	m = decode(t, w)
	if w.Code != 200 || m["url"] != "/preview/demo/nouvelle.html" {
		t.Fatalf("dépôt : %d %v", w.Code, m)
	}
	page := string(sites.pages["demo/nouvelle.html"])
	if !strings.HasPrefix(page, "<!DOCTYPE html>") || !strings.Contains(page, "cdn.tailwindcss.com") || !strings.Contains(page, "<h1>Titre</h1>") || !strings.Contains(page, "<title>Nébula — Titre</title>") {
		t.Errorf("page déposée :\n%s", page)
	}
	pr, _ := e.store.Get(context.Background(), id)
	if !strings.Contains(string(pr.Publish), `"provider":"site"`) || !strings.Contains(string(pr.Publish), `"path":"nouvelle.html"`) {
		t.Errorf("cible non mémorisée : %s", pr.Publish)
	}
	if w := e.do("POST", "/api/projects/"+id+"/site", gin.H{"site": "demo", "path": "nouvelle.html", "overwrite": true}); w.Code != 200 {
		t.Errorf("overwrite : %d", w.Code)
	}
	if w := e.do("POST", "/api/projects/"+id+"/site", gin.H{"site": "autre", "path": "x.html"}); w.Code != 403 {
		t.Errorf("site non attribué : %d", w.Code)
	}
	empty := e.project()
	if w := e.do("POST", "/api/projects/"+empty+"/site", gin.H{"site": "demo", "path": "vide.html"}); w.Code != 422 {
		t.Errorf("projet vide : %d", w.Code)
	}
}
