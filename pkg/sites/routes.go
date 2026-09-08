package sites

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"io/fs"
	"nadaprod/pkg/auth"
	"net/http"
	"os"
	"strings"
)

// ---------- Routes API ----------

// RegisterRoutes : pages et api sont déjà derrière RequireAuth (main.go).
// Ici s'ajoute l'autorisation par site : root voit tout, un utilisateur
// n'atteint que les sites qui lui sont attribués.
func (s *Store) RegisterRoutes(pages, api *gin.RouterGroup, users *auth.UserStore) {
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
	api.POST("/sites", auth.RequireRoot(), func(c *gin.Context) {
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
				"settings": gin.H{"compile": meta.CompileOn(), "pwa": meta.PWAOn(), "gdpr": meta.GDPROn(), "fonts": meta.FontsOn()},
			})
		}
		c.JSON(http.StatusOK, out)
	})

	// Paramétrage du domaine (root uniquement) → pris en compte immédiatement
	// par le middleware.
	api.PUT("/sites/:id/domain", auth.RequireRoot(), func(c *gin.Context) {
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
			Fonts   *bool `json:"fonts"`
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
		if body.Fonts != nil {
			meta.Fonts = body.Fonts
		}
		if err := s.writeMeta(id, meta); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		go s.BuildSite(id)
		c.JSON(http.StatusOK, gin.H{
			"ok":       true,
			"settings": gin.H{"compile": meta.CompileOn(), "pwa": meta.PWAOn(), "gdpr": meta.GDPROn(), "fonts": meta.FontsOn()},
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
	api.DELETE("/sites/:id", auth.RequireRoot(), func(c *gin.Context) {
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
