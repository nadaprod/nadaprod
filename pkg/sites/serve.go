package sites

import (
	"github.com/gin-gonic/gin"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ---------- Routage par domaine (point 3) ----------

func hostname(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(hostport)
}

// HostMiddleware : si le Host de la requête correspond à un domaine
// paramétré, on sert le site statique et rien d'autre. Sinon, l'éditeur.
func (s *Store) HostMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		host := hostname(c.Request.Host)
		s.mu.RLock()
		id, ok := s.domains[host]
		s.mu.RUnlock()
		if !ok {
			c.Next()
			return
		}
		s.serveStatic(c, id, c.Request.URL.Path)
		c.Abort()
	}
}

// serveStatic sert la version publiée : public/ (build) d'abord, repli sur la
// source pour tout ce que le build n'a pas transformé (assets, pages brutes).
func (s *Store) serveStatic(c *gin.Context, id, urlPath string) {
	bases := []string{s.publicDir(id)}
	if src := s.srcDir(id); src != bases[0] {
		bases = append(bases, src)
	}
	for _, base := range bases {
		target, err := safeJoin(base, urlPath)
		if err != nil {
			c.String(http.StatusBadRequest, "chemin invalide")
			return
		}
		if st, err := os.Stat(target); err == nil && st.IsDir() {
			target = filepath.Join(target, "index.html")
		}
		if _, err := os.Stat(target); err == nil {
			http.ServeFile(c.Writer, c.Request, target)
			return
		}
	}
	// c.String(http.StatusNotFound, "404 — page introuvable")
	c.Redirect(301, "/")
}

// serveSource sert la source telle quelle (dev/) : c'est ce que l'éditeur de
// code lit via /preview — jamais les artefacts de build.
func (s *Store) serveSource(c *gin.Context, id, urlPath string) {
	target, err := safeJoin(s.srcDir(id), urlPath)
	if err != nil {
		c.String(http.StatusBadRequest, "chemin invalide")
		return
	}
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		target = filepath.Join(target, "index.html")
	}
	if _, err := os.Stat(target); err != nil {
		c.String(http.StatusNotFound, "404 — page introuvable")
		return
	}
	http.ServeFile(c.Writer, c.Request, target)
}

// ---------- Mode édition (point 2) : la page réelle + un script injecté ----------

var bodyCloseRe = regexp.MustCompile(`(?i)</body>`)

const injectTag = `<link id="__wp_style" rel="stylesheet" href="/static/inject.css"><script id="__wp_inject" src="/static/inject.js" defer></script>`

func (s *Store) serveEdit(c *gin.Context, id, urlPath string) {
	target, err := safeJoin(s.srcDir(id), urlPath)
	if err != nil {
		c.String(http.StatusBadRequest, "chemin invalide")
		return
	}
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		target = filepath.Join(target, "index.html")
	}
	// Les assets (css, js, images, vidéos) passent tels quels :
	// les chemins relatifs du site fonctionnent donc dans l'iframe.
	if !strings.HasSuffix(strings.ToLower(target), ".html") {
		http.ServeFile(c.Writer, c.Request, target)
		return
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		c.String(http.StatusNotFound, "404 — page introuvable")
		return
	}
	var out []byte
	if loc := bodyCloseRe.FindIndex(raw); loc != nil {
		out = append(append(append([]byte{}, raw[:loc[0]]...), []byte(injectTag)...), raw[loc[0]:]...)
	} else {
		out = append(raw, []byte(injectTag)...)
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", out)
}
