package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// Un site = un dossier. Le filesystem est la base de données.
//
//	sites/<id>/site.json   → { "domain": "exemple.com" }
//	sites/<id>/public/…    → le site uploadé, tel quel
const maxUploadBytes = 200 << 20 // 200 Mo

type SiteMeta struct {
	Domain string `json:"domain"`
}

type SiteStore struct {
	root    string
	mu      sync.RWMutex
	domains map[string]string // host → siteID
}

func NewSiteStore(root string) (*SiteStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	s := &SiteStore{root: root, domains: map[string]string{}}
	return s, s.reloadDomains()
}

func (s *SiteStore) siteDir(id string) string   { return filepath.Join(s.root, id) }
func (s *SiteStore) publicDir(id string) string { return filepath.Join(s.root, id, "public") }

func (s *SiteStore) reloadDomains() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	m := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var meta SiteMeta
		if b, err := os.ReadFile(filepath.Join(s.root, e.Name(), "site.json")); err == nil {
			_ = json.Unmarshal(b, &meta)
		}
		if meta.Domain != "" {
			m[strings.ToLower(meta.Domain)] = e.Name()
		}
	}
	s.mu.Lock()
	s.domains = m
	s.mu.Unlock()
	return nil
}

func (s *SiteStore) meta(id string) SiteMeta {
	var meta SiteMeta
	if b, err := os.ReadFile(filepath.Join(s.siteDir(id), "site.json")); err == nil {
		_ = json.Unmarshal(b, &meta)
	}
	return meta
}

// safeJoin résout p sous base et refuse toute évasion (zip-slip, ../…).
func safeJoin(base, p string) (string, error) {
	p = filepath.FromSlash(path.Clean("/" + strings.ReplaceAll(p, "\\", "/")))
	full := filepath.Join(base, p)
	if full != base && !strings.HasPrefix(full, base+string(filepath.Separator)) {
		return "", errors.New("chemin hors du site")
	}
	return full, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(s, ".zip"), ".tar.gz"))
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-")
	if s == "" {
		s = "site"
	}
	return s
}

// ---------- Extraction : zip OU tar.gz, détecté aux magic bytes ----------

func extractArchive(data []byte, dest string) error {
	switch {
	case len(data) > 3 && data[0] == 'P' && data[1] == 'K': // zip
		return extractZip(data, dest)
	case len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b: // gzip → tar.gz
		return extractTarGz(bytes.NewReader(data), dest)
	default:
		return errors.New("format non reconnu : fournissez un .zip ou un .tar.gz")
	}
}

func extractZip(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || isJunk(f.Name) {
			continue
		}
		target, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || isJunk(h.Name) {
			continue
		}
		target, err := safeJoin(dest, h.Name)
		if err != nil {
			return err
		}
		if err := writeFile(target, tr); err != nil {
			return err
		}
	}
}

func isJunk(name string) bool {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	return strings.HasPrefix(name, "__MACOSX") || base == ".DS_Store"
}

func writeFile(target string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// flattenSingleDir : si l'archive contenait un unique dossier racine (cas pp/),
// remonte son contenu d'un cran pour que index.html soit à la racine.
func flattenSingleDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return err
	}
	inner := filepath.Join(dir, entries[0].Name())
	tmp := dir + ".tmp"
	if err := os.Rename(inner, tmp); err != nil {
		return err
	}
	if err := os.Remove(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

// listPages renvoie les chemins relatifs de tous les *.html du site.
func (s *SiteStore) listPages(id string) ([]string, error) {
	root := s.publicDir(id)
	var pages []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".html") {
			rel, _ := filepath.Rel(root, p)
			pages = append(pages, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Slice(pages, func(i, j int) bool { // index.html d'abord, puis alpha
		if (pages[i] == "index.html") != (pages[j] == "index.html") {
			return pages[i] == "index.html"
		}
		return pages[i] < pages[j]
	})
	return pages, err
}

// ---------- Routage par domaine (point 3) ----------

func hostname(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(hostport)
}

// HostMiddleware : si le Host de la requête correspond à un domaine
// paramétré, on sert le site statique et rien d'autre. Sinon, l'éditeur.
func (s *SiteStore) HostMiddleware() gin.HandlerFunc {
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

func (s *SiteStore) serveStatic(c *gin.Context, id, urlPath string) {
	target, err := safeJoin(s.publicDir(id), urlPath)
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

func (s *SiteStore) serveEdit(c *gin.Context, id, urlPath string) {
	target, err := safeJoin(s.publicDir(id), urlPath)
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

// ---------- Routes API ----------

func (s *SiteStore) RegisterRoutes(r *gin.Engine, api *gin.RouterGroup) {
	// Mode édition : sert la page réelle avec l'éditeur injecté.
	r.GET("/edit/:id/*filepath", func(c *gin.Context) {
		s.serveEdit(c, c.Param("id"), c.Param("filepath"))
	})
	// Aperçu brut (sans injection), utile avant de brancher un domaine.
	r.GET("/preview/:id/*filepath", func(c *gin.Context) {
		s.serveStatic(c, c.Param("id"), c.Param("filepath"))
	})

	// Import : un champ multipart "archive" (.zip ou .tar.gz).
	api.POST("/sites", func(c *gin.Context) {
		file, header, err := c.Request.FormFile("archive")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "champ 'archive' manquant"})
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
		if err != nil || len(data) > maxUploadBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "archive illisible ou > 200 Mo"})
			return
		}

		id := slugify(header.Filename)
		if _, err := os.Stat(s.siteDir(id)); err == nil {
			id = fmt.Sprintf("%s-%d", id, os.Getpid()%10000+len(data)%1000)
		}
		pub := s.publicDir(id)
		if err := os.MkdirAll(pub, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := extractArchive(data, pub); err != nil {
			os.RemoveAll(s.siteDir(id))
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := flattenSingleDir(pub); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		pages, _ := s.listPages(id)
		if len(pages) == 0 {
			os.RemoveAll(s.siteDir(id))
			c.JSON(http.StatusBadRequest, gin.H{"error": "aucun fichier .html dans l'archive"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id, "pages": pages})
	})

	// Liste des sites.
	api.GET("/sites", func(c *gin.Context) {
		entries, err := os.ReadDir(s.root)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		out := []gin.H{}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			pages, _ := s.listPages(e.Name())
			out = append(out, gin.H{"id": e.Name(), "domain": s.meta(e.Name()).Domain, "pages": pages})
		}
		c.JSON(http.StatusOK, out)
	})

	// Paramétrage du domaine → pris en compte immédiatement par le middleware.
	api.PUT("/sites/:id/domain", func(c *gin.Context) {
		var body struct {
			Domain string `json:"domain"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		meta := SiteMeta{Domain: hostname(strings.TrimSpace(body.Domain))}
		b, _ := json.MarshalIndent(meta, "", "  ")
		if err := os.WriteFile(filepath.Join(s.siteDir(c.Param("id")), "site.json"), b, 0o644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		_ = s.reloadDomains()
		c.JSON(http.StatusOK, gin.H{"ok": true, "domain": meta.Domain})
	})

	// Sauvegarde d'une page éditée : on réécrit le fichier, point final.
	api.PUT("/sites/:id/file", func(c *gin.Context) {
		var body struct {
			Path string `json:"path"`
			HTML string `json:"html"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if !strings.HasSuffix(strings.ToLower(body.Path), ".html") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "seuls les fichiers .html sont éditables"})
			return
		}
		target, err := safeJoin(s.publicDir(c.Param("id")), body.Path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := os.WriteFile(target, []byte(body.HTML), 0o644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Suppression d'un site.
	api.DELETE("/sites/:id", func(c *gin.Context) {
		target, err := safeJoin(s.root, c.Param("id"))
		if err != nil || target == s.root {
			c.JSON(http.StatusBadRequest, gin.H{"error": "site invalide"})
			return
		}
		if err := os.RemoveAll(target); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		_ = s.reloadDomains()
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}
