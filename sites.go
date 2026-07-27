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
	"log"
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
//	sites/<id>/site.json   → { "domain": "exemple.com", "compile": true, … }
//	sites/<id>/dev/…       → la source éditable (uploadée, retouchée)
//	sites/<id>/public/…    → la sortie publiée (build.go), régénérable
//
// Le service sur domaine essaie public/ puis retombe sur dev/ : seuls les
// fichiers transformés sont dupliqués, jamais les assets.
const (
	maxUploadBytes  = 200 << 20 // 200 Mo (archive compressée)
	maxExtractBytes = 1 << 30   // 1 Go décompressé (protection zip bomb)
)

var errArchiveTooLarge = errors.New("archive décompressée trop volumineuse (limite 1 Go)")

// siteIDRe : les ids effectivement produits (slugify) ou créés à la main
// (sites cachés « .famille », « l3dlp.com ») — jamais « . », « .. » ni de
// séparateur. Complète safeJoin sur toutes les routes :id.
var siteIDRe = regexp.MustCompile(`^\.?[a-z0-9][a-z0-9._-]*$`)

func validSiteID(id string) bool { return len(id) <= 128 && siteIDRe.MatchString(id) }

// domainRe : nom d'hôte complet uniquement (labels alphanumériques avec
// tirets internes, TLD alphabétique) — pas d'IP, pas de « localhost ».
var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func validDomain(d string) bool { return len(d) <= 253 && domainRe.MatchString(d) }

type SiteMeta struct {
	Domain string `json:"domain"`
	// Réglages de publication (nil = activé — le défaut « zéro effort »).
	Compile *bool `json:"compile,omitempty"` // Tailwind CDN → CSS compilé
	PWA     *bool `json:"pwa,omitempty"`     // manifest + service worker générés
	GDPR    *bool `json:"gdpr,omitempty"`    // bandeau « tout est local »
}

func onByDefault(p *bool) bool     { return p == nil || *p }
func (m SiteMeta) CompileOn() bool { return onByDefault(m.Compile) }
func (m SiteMeta) PWAOn() bool     { return onByDefault(m.PWA) }
func (m SiteMeta) GDPROn() bool    { return onByDefault(m.GDPR) }

type SiteStore struct {
	root       string
	twBin      string // binaire standalone Tailwind ("" = compilation désactivée)
	mu         sync.RWMutex
	domains    map[string]string // host → siteID
	buildLocks sync.Map          // siteID → *sync.Mutex (build.go)
}

func NewSiteStore(root, twBin string) (*SiteStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	s := &SiteStore{root: root, twBin: twBin, domains: map[string]string{}}
	s.migrateLayout()
	return s, s.reloadDomains()
}

// migrateLayout : les sites d'avant le pipeline de build n'ont qu'un public/.
// On en fait la source (dev/) ; public/ sera régénéré par BuildAll.
func (s *SiteStore) migrateLayout() {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dev, pub := s.devDir(e.Name()), s.publicDir(e.Name())
		if _, err := os.Stat(dev); !os.IsNotExist(err) {
			continue
		}
		if _, err := os.Stat(pub); err == nil {
			if err := os.Rename(pub, dev); err != nil {
				log.Printf("migration %s : public/ → dev/ impossible : %v", e.Name(), err)
			}
		}
	}
}

func (s *SiteStore) siteDir(id string) string   { return filepath.Join(s.root, id) }
func (s *SiteStore) devDir(id string) string    { return filepath.Join(s.root, id, "dev") }
func (s *SiteStore) publicDir(id string) string { return filepath.Join(s.root, id, "public") }

// srcDir : la source éditable. dev/ après migration ; repli sur public/ pour
// un site legacy dont la migration aurait échoué (on n'y écrit alors jamais
// de build par-dessus — voir BuildSite).
func (s *SiteStore) srcDir(id string) string {
	if d := s.devDir(id); dirExists(d) {
		return d
	}
	return s.publicDir(id)
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

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

func (s *SiteStore) writeMeta(id string, meta SiteMeta) error {
	b, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(filepath.Join(s.siteDir(id), "site.json"), b, 0o644)
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
	s = strings.ToLower(s)
	for _, ext := range []string{".zip", ".tar.gz", ".tgz"} {
		s = strings.TrimSuffix(s, ext)
	}
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-")
	if s == "" {
		s = "site"
	}
	return s
}

// ---------- Extraction : zip OU tar.gz, détecté aux magic bytes ----------

func extractArchive(data []byte, dest string) error {
	budget := int64(maxExtractBytes)
	switch {
	case len(data) > 3 && data[0] == 'P' && data[1] == 'K': // zip
		return extractZip(data, dest, &budget)
	case len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b: // gzip → tar.gz
		return extractTarGz(bytes.NewReader(data), dest, &budget)
	default:
		return errors.New("format non reconnu : fournissez un .zip ou un .tar.gz")
	}
}

func extractZip(data []byte, dest string, budget *int64) error {
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
		err = writeFileCapped(target, rc, budget)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(r io.Reader, dest string, budget *int64) error {
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
		if err := writeFileCapped(target, tr, budget); err != nil {
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

// writeFileCapped : writeFile en décomptant un budget global d'extraction.
// On compte les octets réellement écrits (les tailles annoncées par une
// archive peuvent mentir) — le budget épuisé, l'extraction échoue.
func writeFileCapped(target string, r io.Reader, budget *int64) error {
	if *budget <= 0 {
		return errArchiveTooLarge
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(r, *budget+1))
	*budget -= n
	if err != nil {
		return err
	}
	if *budget < 0 {
		return errArchiveTooLarge
	}
	return nil
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

// listPages renvoie les chemins relatifs de tous les *.html de la source.
func (s *SiteStore) listPages(id string) ([]string, error) {
	root := s.srcDir(id)
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

// serveStatic sert la version publiée : public/ (build) d'abord, repli sur la
// source pour tout ce que le build n'a pas transformé (assets, pages brutes).
func (s *SiteStore) serveStatic(c *gin.Context, id, urlPath string) {
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
func (s *SiteStore) serveSource(c *gin.Context, id, urlPath string) {
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

func (s *SiteStore) serveEdit(c *gin.Context, id, urlPath string) {
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

// ---------- Routes API ----------

// RegisterRoutes : pages et api sont déjà derrière RequireAuth (main.go).
// Ici s'ajoute l'autorisation par site : root voit tout, un utilisateur
// n'atteint que les sites qui lui sont attribués.
func (s *SiteStore) RegisterRoutes(pages, api *gin.RouterGroup, users *UserStore) {
	// allowed : 400 si l'id n'a pas la forme d'un site (défense en profondeur
	// avec safeJoin), 403 si le site n'est pas accessible au compte connecté.
	allowed := func(c *gin.Context, id string) bool {
		api := strings.HasPrefix(c.Request.URL.Path, "/api/")
		if !validSiteID(id) {
			if api {
				c.JSON(http.StatusBadRequest, gin.H{"error": "site invalide"})
			} else {
				c.String(http.StatusBadRequest, "400 — site invalide")
			}
			return false
		}
		if users.CanAccess(c.GetString("user"), id) {
			return true
		}
		if api {
			c.JSON(http.StatusForbidden, gin.H{"error": "accès refusé à ce site"})
		} else {
			c.String(http.StatusForbidden, "403 — accès refusé à ce site")
		}
		return false
	}

	// Mode édition : sert la page réelle avec l'éditeur injecté.
	pages.GET("/edit/:id/*filepath", func(c *gin.Context) {
		if !allowed(c, c.Param("id")) {
			return
		}
		s.serveEdit(c, c.Param("id"), c.Param("filepath"))
	})
	// Aperçu brut de la source (sans injection ni artefacts de build) :
	// c'est aussi ce que l'éditeur de code Monaco lit et réécrit.
	pages.GET("/preview/:id/*filepath", func(c *gin.Context) {
		if !allowed(c, c.Param("id")) {
			return
		}
		s.serveSource(c, c.Param("id"), c.Param("filepath"))
	})

	// Import (root uniquement) : un champ multipart "archive" (.zip ou .tar.gz).
	api.POST("/sites", RequireRoot(), func(c *gin.Context) {
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

		// L'ID est réclamé atomiquement par os.Mkdir : deux uploads du même
		// nom (même concurrents) obtiennent des suffixes -2, -3, … distincts.
		base := slugify(header.Filename)
		id := base
		for i := 2; ; i++ {
			err := os.Mkdir(s.siteDir(id), 0o755)
			if err == nil {
				break
			}
			if !errors.Is(err, fs.ErrExist) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			id = fmt.Sprintf("%s-%d", base, i)
		}
		dev := s.devDir(id)
		if err := os.MkdirAll(dev, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := extractArchive(data, dev); err != nil {
			os.RemoveAll(s.siteDir(id))
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := flattenSingleDir(dev); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		pages, _ := s.listPages(id)
		if len(pages) == 0 {
			os.RemoveAll(s.siteDir(id))
			c.JSON(http.StatusBadRequest, gin.H{"error": "aucun fichier .html dans l'archive"})
			return
		}
		go s.BuildSite(id) // publication en arrière-plan, la réponse n'attend pas
		c.JSON(http.StatusOK, gin.H{"id": id, "pages": pages})
	})

	// Liste des sites accessibles au compte connecté.
	api.GET("/sites", func(c *gin.Context) {
		entries, err := os.ReadDir(s.root)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		out := []gin.H{}
		for _, e := range entries {
			if !e.IsDir() || !users.CanAccess(c.GetString("user"), e.Name()) {
				continue
			}
			pages, _ := s.listPages(e.Name())
			meta := s.meta(e.Name())
			out = append(out, gin.H{
				"id": e.Name(), "domain": meta.Domain, "pages": pages,
				"settings": gin.H{"compile": meta.CompileOn(), "pwa": meta.PWAOn(), "gdpr": meta.GDPROn()},
			})
		}
		c.JSON(http.StatusOK, out)
	})

	// Paramétrage du domaine (root uniquement) → pris en compte immédiatement
	// par le middleware.
	api.PUT("/sites/:id/domain", RequireRoot(), func(c *gin.Context) {
		var body struct {
			Domain string `json:"domain"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		id := c.Param("id")
		if !allowed(c, id) { // root ici (RequireRoot) : ne valide que l'id
			return
		}
		domain := hostname(strings.TrimSpace(body.Domain))
		if domain != "" && !validDomain(domain) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "domaine invalide — attendu un nom d'hôte comme exemple.fr"})
			return
		}
		meta := s.meta(id) // préserve les réglages de publication
		meta.Domain = domain
		if err := s.writeMeta(id, meta); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		_ = s.reloadDomains()
		go s.BuildSite(id) // le manifest PWA porte le nom du domaine
		c.JSON(http.StatusOK, gin.H{"ok": true, "domain": meta.Domain})
	})

	// Réglages de publication (panneau ⚙ de l'éditeur) → rebuild du site.
	api.PUT("/sites/:id/settings", func(c *gin.Context) {
		var body struct {
			Compile *bool `json:"compile"`
			PWA     *bool `json:"pwa"`
			GDPR    *bool `json:"gdpr"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		id := c.Param("id")
		if !allowed(c, id) {
			return
		}
		meta := s.meta(id)
		if body.Compile != nil {
			meta.Compile = body.Compile
		}
		if body.PWA != nil {
			meta.PWA = body.PWA
		}
		if body.GDPR != nil {
			meta.GDPR = body.GDPR
		}
		if err := s.writeMeta(id, meta); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		go s.BuildSite(id)
		c.JSON(http.StatusOK, gin.H{
			"ok":       true,
			"settings": gin.H{"compile": meta.CompileOn(), "pwa": meta.PWAOn(), "gdpr": meta.GDPROn()},
		})
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
		id := c.Param("id")
		if !allowed(c, id) {
			return
		}
		target, err := safeJoin(s.srcDir(id), body.Path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := os.WriteFile(target, []byte(body.HTML), 0o644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Republication en arrière-plan (~3 s de compilation par page) :
		// la sauvegarde reste instantanée, la version publiée suit juste après.
		go s.RebuildPage(id, body.Path)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Suppression d'un site (root uniquement).
	api.DELETE("/sites/:id", RequireRoot(), func(c *gin.Context) {
		target, err := safeJoin(s.root, c.Param("id"))
		if !validSiteID(c.Param("id")) || err != nil || target == s.root {
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
