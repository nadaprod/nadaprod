package stats

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// AppCounts : l'état actuel de l'application (studio + sites), fourni par main.
type AppCounts struct {
	Projects     int            `json:"projects"`
	Blocks       int            `json:"blocks"`
	Versions     map[string]int `json:"versions"`
	Translations map[string]int `json:"translations"`
	Sites        int            `json:"sites"`
	LiveSites    int            `json:"live_sites"`
	Pages        int            `json:"pages"`
}

// Deps : ce dont les routes ont besoin (Counts est optionnel).
type Deps struct {
	Store  *Store
	Counts func(ctx context.Context) (AppCounts, error)
	TTL    time.Duration // cache de la réponse publique (≤ 0 ⇒ 60 s)
}

// Register : GET /stats (public, mis en cache) sur public ; GET /stats/sites
// (détail par site) sur root, groupe déjà protégé par l'appelant.
func Register(public, root *gin.RouterGroup, d Deps) {
	if d.TTL <= 0 {
		d.TTL = 60 * time.Second
	}
	var (
		mu     sync.Mutex
		cached gin.H
		until  time.Time
	)
	public.GET("/stats", func(c *gin.Context) {
		mu.Lock()
		defer mu.Unlock()
		if cached == nil || time.Now().After(until) {
			sum, err := d.Store.Summary(c.Request.Context(), 30)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "internal"})
				return
			}
			app := AppCounts{Versions: map[string]int{}, Translations: map[string]int{}}
			if d.Counts != nil {
				if app, err = d.Counts(c.Request.Context()); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "internal"})
					return
				}
			}
			if app.Versions == nil {
				app.Versions = map[string]int{}
			}
			if app.Translations == nil {
				app.Translations = map[string]int{}
			}
			cached = gin.H{"since": sum.Since, "updated_at": time.Now().UTC().Format(time.RFC3339),
				"ai": sum.AI, "static": sum.Static, "app": app}
			until = time.Now().Add(d.TTL)
		}
		c.Header("Cache-Control", "public, max-age=60")
		c.JSON(http.StatusOK, cached)
	})
	root.GET("/stats/sites", func(c *gin.Context) {
		sites, err := d.Store.SiteSummary(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "internal"})
			return
		}
		c.JSON(http.StatusOK, sites)
	})
}
