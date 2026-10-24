package projects

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ---------- Routes API ----------
//
// api est déjà derrière RequireAuth (main.go) : "user" et "isRoot" sont dans
// le contexte. Un projet appartient à son créateur ; root accède à tout par
// identifiant, mais sa liste ne montre que les siens sauf ?all=1.

// Fail traduit une erreur du store en réponse HTTP. Le champ "code" permet à
// l'UI de réagir (conflit ⇒ proposer de recharger) sans analyser le message.
func Fail(c *gin.Context, err error) {
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, ErrLimit):
		status, code = http.StatusRequestEntityTooLarge, "limit"
	}
	c.JSON(status, gin.H{"error": err.Error(), "code": code})
}

// reply : v si l'opération a réussi, sinon l'erreur traduite par fail.
func reply(c *gin.Context, status int, v any, err error) {
	if err != nil {
		Fail(c, err)
		return
	}
	c.JSON(status, v)
}

// bind lit le corps JSON ; s'il est invalide, répond 400 et renvoie false.
func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "requête invalide : " + err.Error(), "code": "invalid"})
		return false
	}
	return true
}

// Allowed : le projet existe et le compte connecté peut y toucher. Répond
// lui-même en cas de refus. Exportée : l'export et la publication (main.go)
// appliquent la même règle.
func (s *Store) Allowed(c *gin.Context, id string) bool {
	if !ValidID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "projet invalide", "code": "invalid"})
		return false
	}
	owner, err := s.Owner(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return false
	}
	if owner != c.GetString("user") && !c.GetBool("isRoot") {
		// 404 plutôt que 403 : ne pas confirmer l'existence d'un projet d'autrui.
		Fail(c, ErrNotFound)
		return false
	}
	return true
}

func (s *Store) RegisterRoutes(api *gin.RouterGroup) {
	// Le corps d'un projet importé peut être gros (60 blocs) mais reste borné.
	const maxBody = 24 << 20
	limit := func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
	}
	// owned : le handler ne s'exécute que si le compte peut toucher au projet :id.
	owned := func(h func(c *gin.Context, ctx context.Context, id string)) gin.HandlerFunc {
		return func(c *gin.Context) {
			if id := c.Param("id"); s.Allowed(c, id) {
				h(c, c.Request.Context(), id)
			}
		}
	}

	api.GET("/projects", func(c *gin.Context) {
		all := c.GetBool("isRoot") && c.Query("all") == "1"
		list, err := s.List(c.Request.Context(), c.GetString("user"), all)
		reply(c, http.StatusOK, list, err)
	})

	// Création : projet vide, import JSON ou migration du localStorage.
	api.POST("/projects", limit, func(c *gin.Context) {
		var in ProjectInput
		if !bind(c, &in) {
			return
		}
		p, err := s.Create(c.Request.Context(), c.GetString("user"), in)
		reply(c, http.StatusCreated, p, err)
	})

	api.GET("/projects/:id", owned(func(c *gin.Context, ctx context.Context, id string) {
		p, err := s.Get(ctx, id)
		reply(c, http.StatusOK, p, err)
	}))

	api.PUT("/projects/:id", limit, owned(func(c *gin.Context, ctx context.Context, id string) {
		var in MetaInput
		if !bind(c, &in) {
			return
		}
		p, err := s.UpdateMeta(ctx, id, in)
		// Les blocs ne sont pas renvoyés : le client les connaît déjà.
		reply(c, http.StatusOK, gin.H{"ok": true, "rev": p.Rev, "updated_at": p.UpdatedAt}, err)
	}))

	api.DELETE("/projects/:id", owned(func(c *gin.Context, ctx context.Context, id string) {
		reply(c, http.StatusOK, gin.H{"ok": true}, s.Delete(ctx, id))
	}))

	// Écriture d'un bloc (création ou mise à jour) — historise si le HTML change.
	api.PUT("/projects/:id/blocks/:bid", limit, owned(func(c *gin.Context, ctx context.Context, id string) {
		var body struct {
			BlockInput
			Rev *int64 `json:"rev"`
		}
		if !bind(c, &body) {
			return
		}
		body.ID = c.Param("bid")
		b, rev, err := s.SaveBlock(ctx, id, body.BlockInput, body.Rev)
		b.HTML = "" // le client vient de l'envoyer : inutile de le lui renvoyer
		reply(c, http.StatusOK, gin.H{"ok": true, "rev": rev, "block": b}, err)
	}))

	api.GET("/projects/:id/blocks/:bid/versions", owned(func(c *gin.Context, ctx context.Context, id string) {
		vs, err := s.Versions(ctx, id, c.Param("bid"))
		reply(c, http.StatusOK, vs, err)
	}))

	// Déplacement du pointeur de version : {"version_id": N} (restaurer) ou
	// {"step": -1|1} (annuler / rétablir).
	api.POST("/projects/:id/blocks/:bid/checkout", owned(func(c *gin.Context, ctx context.Context, id string) {
		var body struct {
			VersionID int64  `json:"version_id"`
			Step      int    `json:"step"`
			Rev       *int64 `json:"rev"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || (body.VersionID == 0) == (body.Step == 0) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "attendu : version_id ou step", "code": "invalid"})
			return
		}
		bid := c.Param("bid")
		var (
			b   Block
			rev int64
			err error
		)
		if body.Step != 0 {
			b, rev, err = s.Step(ctx, id, bid, body.Step, body.Rev)
		} else {
			b, rev, err = s.Checkout(ctx, id, bid, body.VersionID, body.Rev)
		}
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "aucune version dans cette direction", "code": "no_version"})
			return
		}
		reply(c, http.StatusOK, gin.H{"ok": true, "rev": rev, "block": b}, err)
	}))

	// Export JSON du projet (sauvegarde / transfert) — réimportable tel quel
	// par POST /projects.
	api.GET("/projects/:id/export.json", owned(func(c *gin.Context, ctx context.Context, id string) {
		p, err := s.Get(ctx, id)
		if err != nil {
			Fail(c, err)
			return
		}
		blocks := make([]BlockInput, 0, len(p.Blocks))
		for _, b := range p.Blocks {
			blocks = append(blocks, BlockInput{ID: b.ID, Type: b.Type, HTML: b.HTML, Prompt: b.Prompt})
		}
		c.Header("Content-Disposition", "attachment; filename="+strconv.Quote(p.ID+".nadaprod.json"))
		c.JSON(http.StatusOK, gin.H{
			"format": "nadaprod-project", "version": 1,
			"name": p.Name, "brand": p.Brand, "theme": p.Theme, "seo": p.SEO, "blocks": blocks,
		})
	}))
}
