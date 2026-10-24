package sites

import (
	"github.com/gin-gonic/gin"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
		start := time.Now()
		s.serveStatic(c, id, c.Request.URL.Path)
		c.Abort()
		if s.OnHit != nil {
			status := c.Writer.Status()
			html := status >= 200 && status < 300 && strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/html")
			s.OnHit(id, status, int64(max(c.Writer.Size(), 0)), time.Since(start), html, c.Request.UserAgent())
		}
	}
}

// resolve : chemin d'URL → fichier sous base (safeJoin), un dossier
// désignant son index.html.
func resolve(base, urlPath string) (string, error) {
	target, err := safeJoin(base, urlPath)
	if err != nil {
		return "", err
	}
	if dirExists(target) {
		target = filepath.Join(target, "index.html")
	}
	return target, nil
}

// serveStatic sert la version publiée : public/ (build) d'abord, repli sur la
// source pour tout ce que le build n'a pas transformé (assets, pages brutes).
func (s *Store) serveStatic(c *gin.Context, id, urlPath string) {
	bases := []string{s.publicDir(id)}
	if src := s.srcDir(id); src != bases[0] {
		bases = append(bases, src)
	}
	for _, base := range bases {
		target, err := resolve(base, urlPath)
		if err != nil {
			c.String(http.StatusBadRequest, "chemin invalide")
			return
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
	target, err := resolve(s.srcDir(id), urlPath)
	if err != nil {
		c.String(http.StatusBadRequest, "chemin invalide")
		return
	}
	if _, err := os.Stat(target); err != nil {
		c.String(http.StatusNotFound, "404 — page introuvable")
		return
	}
	http.ServeFile(c.Writer, c.Request, target)
}

// ---------- Mode édition (point 2) : la page réelle + un script injecté ----------

var bodyCloseRe = regexp.MustCompile(`(?i)</body>`)

const injectTag = `<link id="__wp_style" rel="stylesheet" href="/static/inject.css"><script id="__wp_inject" src="/static/inject.js?v=inert" defer></script>`

func (s *Store) serveEdit(c *gin.Context, id, urlPath string) {
	target, err := resolve(s.srcDir(id), urlPath)
	if err != nil {
		c.String(http.StatusBadRequest, "chemin invalide")
		return
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
	// Scripts inertes (inert.go) : le DOM sérialisé à l'enregistrement est
	// celui de la source, pas celui qu'ils auraient modifié.
	c.Data(http.StatusOK, "text/html; charset=utf-8", insertBefore(inertScripts(raw), bodyCloseRe, injectTag))
}
