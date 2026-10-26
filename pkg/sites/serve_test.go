package sites

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Une page introuvable sur un domaine branché ne boucle jamais : page 404 du
// site si elle existe, sinon retour à l'accueil s'il existe, sinon 404 nu.
// (Un site importé sans index.html renvoyait « / » vers « / » à l'infini.)
func TestServeStaticMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	st, err := NewStore(t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	site := func(id, domain string, files map[string]string) {
		dev := filepath.Join(st.root, id, "dev")
		for rel, body := range files {
			p := filepath.Join(dev, rel)
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(body), 0o644)
		}
		os.MkdirAll(dev, 0o755)
		if err := st.writeMeta(id, Meta{Domain: domain}); err != nil {
			t.Fatal(err)
		}
	}
	site("complet", "complet.test", map[string]string{"index.html": "<p>accueil</p>", "a/index.html": "<p>a</p>"})
	site("sansaccueil", "sansaccueil.test", map[string]string{"404.html": "<p>perdu</p>", "geometry.html": "<p>g</p>"})
	site("nu", "nu.test", map[string]string{"page.html": "<p>p</p>"})
	if err := st.reloadDomains(); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(st.HostMiddleware())
	get := func(host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	cases := []struct {
		host, path string
		code       int
		body, loc  string
	}{
		{"complet.test", "/", 200, "accueil", ""},
		{"complet.test", "/a/", 200, "<p>a</p>", ""},
		{"complet.test", "/absent", 301, "", "/"},
		{"sansaccueil.test", "/", 404, "perdu", ""},       // bouclait
		{"sansaccueil.test", "/absent", 404, "perdu", ""}, // la 404 du site, pas une redirection
		{"sansaccueil.test", "/geometry.html", 200, "<p>g</p>", ""},
		{"nu.test", "/", 404, "introuvable", ""}, // ni accueil ni 404 : 404 nu, sans boucle
		{"nu.test", "/absent", 404, "introuvable", ""},
		{"complet.test", "/../secret", 0, "", ""}, // chemin hors du site : refusé ou nettoyé, jamais servi
	}
	for _, tc := range cases {
		w := get(tc.host, tc.path)
		if tc.code == 0 {
			if strings.Contains(w.Body.String(), "secret") {
				t.Errorf("%s%s : fuite", tc.host, tc.path)
			}
			continue
		}
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.body) || (tc.loc != "" && w.Header().Get("Location") != tc.loc) {
			t.Errorf("%s%s : %d %q loc=%q", tc.host, tc.path, w.Code, w.Body.String(), w.Header().Get("Location"))
		}
	}
}
