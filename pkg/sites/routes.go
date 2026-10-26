package sites

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"nadaprod/pkg/auth"
	"net/http"
	"os"
	"strings"
)

// ---------- Routes API ----------

// settings : les réglages de publication effectifs, tels que l'UI les affiche.
func (m Meta) settings() gin.H {
	return gin.H{"compile": m.CompileOn(), "pwa": m.PWAOn(), "gdpr": m.GDPROn(), "fonts": m.FontsOn()}
}

// RegisterRoutes : pages et api sont déjà derrière RequireAuth (main.go).
// Ici s'ajoute l'autorisation par site : root voit tout, un utilisateur
// n'atteint que les sites qui lui sont attribués.
func (s *Store) RegisterRoutes(pages, api *gin.RouterGroup, users *auth.UserStore) {
	// allowed : 400 si l'id n'a pas la forme d'un site (défense en profondeur
	// avec safeJoin), 403 si le site n'est pas accessible au compte connecté.
	allowed := func(c *gin.Context, id string) bool {
		deny := func(code int, msg string) bool {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.JSON(code, gin.H{"error": msg})
			} else {
				c.String(code, fmt.Sprintf("%d — %s", code, msg))
			}
			return false
		}
		if !validSiteID(id) {
			return deny(http.StatusBadRequest, "site invalide")
		}
		if !users.CanAccess(c.GetString("user"), id) {
			return deny(http.StatusForbidden, "accès refusé à ce site")
		}
		return true
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

		// L'ID est réclamé atomiquement (newSite) : deux uploads du même nom,
		// même concurrents, obtiennent des suffixes -2, -3, … distincts.
		id, err := s.newSite(slugify(header.Filename))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		dev := s.devDir(id)
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

	// Site vide, créé de zéro (root) : le studio y déposera sa première page.
	api.POST("/sites/new", auth.RequireRoot(), func(c *gin.Context) {
		var body struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "nom du site manquant"})
			return
		}
		id, err := s.CreateSite(body.Name)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id, "pages": []string{}})
	})

	// Nouvelle page dans un site : copie d'une page existante (from) ou page
	// vierge. Pour partir du studio, le client ouvre /editor?site=…&path=….
	api.POST("/sites/:id/pages", func(c *gin.Context) {
		var body struct {
			Name string `json:"name"` // « a-propos » ou « blog/mon-article.html »
			From string `json:"from"` // page à copier (optionnel)
		}
		if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "nom de page manquant"})
			return
		}
		id := c.Param("id")
		if !allowed(c, id) {
			return
		}
		rel := body.Name
		if !strings.Contains(rel, "/") && !strings.HasSuffix(strings.ToLower(rel), ".html") {
			rel = PageName(rel)
		}
		var (
			out string
			err error
		)
		if body.From != "" {
			out, err = s.CopyPage(id, body.From, rel)
		} else {
			out, err = s.SavePage(id, rel, BlankPage(strings.TrimSuffix(body.Name, ".html")), false)
		}
		switch {
		case errors.Is(err, ErrPageExists):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, ErrPageNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "page source introuvable"})
		case err != nil:
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusOK, gin.H{"ok": true, "path": out})
		}
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
			pages, sp, _ := s.scan(e.Name())
			meta := s.meta(e.Name())
			created, updated := siteDates(meta, sp)
			out = append(out, gin.H{
				"id": e.Name(), "domain": meta.Domain, "pages": pages,
				"settings": meta.settings(), "created_at": created, "updated_at": updated,
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
			"settings": meta.settings(),
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
		if !Editable(body.Path) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "seuls les fichiers texte (html, css, js, json, svg…) sont éditables"})
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
		data := []byte(body.HTML)
		if strings.HasSuffix(strings.ToLower(body.Path), ".html") {
			// Filet de sécurité : un inject.js en cache (antérieur à inert.go)
			// renverrait les scripts encore inertes.
			data = restoreScripts(data)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Republication en arrière-plan (~3 s de compilation par page) :
		// la sauvegarde reste instantanée, la version publiée suit juste après.
		// Un asset (CSS, JS…) n'est pas transformé : public/ retombe sur dev/,
		// il est servi tel quel dès l'écriture.
		if strings.HasSuffix(strings.ToLower(body.Path), ".html") {
			go s.RebuildPage(id, body.Path)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Fichiers de l'atelier de code : CSS/JS locaux référencés par la page,
	// puis les autres CSS/JS du site.
	api.GET("/sites/:id/assets", func(c *gin.Context) {
		id := c.Param("id")
		if !allowed(c, id) {
			return
		}
		referenced, others, err := s.Assets(id, c.Query("page"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"referenced": referenced, "others": others})
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
