package sites

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type hit struct {
	site   string
	status int
	bytes  int64
	html   bool
	ua     string
}

// HostMiddleware signale chaque requête servie sur un domaine branché, et
// seulement celles-là ; sans OnHit, rien ne change.
func TestHostMiddlewareOnHit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	st, err := NewStore(t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	dev := filepath.Join(st.root, "demo", "dev")
	os.MkdirAll(dev, 0o755)
	os.WriteFile(filepath.Join(dev, "index.html"), []byte("<!doctype html><title>x</title><p>Bonjour</p>"), 0o644)
	os.WriteFile(filepath.Join(dev, "site.css"), []byte("p{color:red}"), 0o644)
	if err := st.writeMeta("demo", Meta{Domain: "demo.test"}); err != nil {
		t.Fatal(err)
	}
	if err := st.reloadDomains(); err != nil {
		t.Fatal(err)
	}

	var hits []hit
	r := gin.New()
	r.Use(st.HostMiddleware())
	r.GET("/app", func(c *gin.Context) { c.String(200, "app") })
	get := func(host, path, ua string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		req.Header.Set("User-Agent", ua)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Sans OnHit : service inchangé.
	if w := get("demo.test", "/", "Mozilla"); w.Code != 200 || w.Body.String() == "" {
		t.Fatalf("sans OnHit : %d", w.Code)
	}
	st.OnHit = func(site string, status int, bytes int64, dur time.Duration, html bool, ua string) {
		if dur < 0 {
			t.Errorf("durée négative")
		}
		hits = append(hits, hit{site, status, bytes, html, ua})
	}
	page := get("demo.test", "/", "Mozilla/5.0")
	get("demo.test:443", "/site.css", "Mozilla/5.0")
	get("demo.test", "/absent.html", "curl/8")
	if w := get("app.example", "/app", "Mozilla"); w.Body.String() != "app" {
		t.Fatal("hôte non branché : la requête doit continuer vers l'app")
	}

	if len(hits) != 3 {
		t.Fatalf("hits : %+v", hits)
	}
	if h := hits[0]; h.site != "demo" || h.status != 200 || !h.html || h.bytes != int64(page.Body.Len()) || h.ua != "Mozilla/5.0" {
		t.Errorf("page : %+v", h)
	}
	if h := hits[1]; h.html || h.status != 200 || h.bytes == 0 {
		t.Errorf("css : %+v", h)
	}
	if h := hits[2]; h.html || h.status < 300 || h.ua != "curl/8" { // absent : redirection ou 404, jamais une page
		t.Errorf("absent : %+v", h)
	}
}
