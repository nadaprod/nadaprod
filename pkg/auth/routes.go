package auth

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
	"time"
)

const failedLoginWait = 300 * time.Millisecond

// ---------- Routes ----------

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// reply : {"ok": true}, ou 400 avec le message d'erreur.
func reply(c *gin.Context, err error) {
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *UserStore) setSessionCookie(c *gin.Context, value string, maxAge int) {
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookie, value, maxAge, "/", "", secure, true)
}

// RegisterAuthRoutes : public = login + liste d'attente ; authed = le reste.
func (s *UserStore) RegisterAuthRoutes(r *gin.Engine, api, apiAuthed *gin.RouterGroup) {
	api.POST("/login", func(c *gin.Context) {
		var body credentials
		if err := c.ShouldBindJSON(&body); err != nil {
			reply(c, err)
			return
		}
		if !s.Authenticate(body.Email, body.Password) {
			time.Sleep(failedLoginWait) // amortit la force brute
			c.JSON(http.StatusUnauthorized, gin.H{"error": "email ou mot de passe incorrect"})
			return
		}
		email := normalizeEmail(body.Email)
		s.setSessionCookie(c, s.MakeSession(email), int(sessionTTL.Seconds()))
		c.JSON(http.StatusOK, gin.H{"ok": true, "email": email, "root": s.IsRoot(email)})
	})

	// Candidatures publiques (formulaire /waiting-list). Le champ "website"
	// est un pot de miel : rempli ⇒ bot ⇒ accepté sans stocker.
	r.POST("/waiting-list", func(c *gin.Context) {
		var body struct {
			Name    string `json:"name"`
			Email   string `json:"email"`
			Message string `json:"message"`
			Website string `json:"website"` // honeypot
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "requête invalide"})
			return
		}
		email := normalizeEmail(body.Email)
		if !emailRe.MatchString(email) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email invalide"})
			return
		}
		if body.Website == "" { // humains uniquement
			err := s.AddToWaitlist(WaitlistEntry{
				Name:    truncate(strings.TrimSpace(body.Name), 100),
				Email:   truncate(email, 200),
				Message: truncate(strings.TrimSpace(body.Message), 2000),
				Date:    time.Now().Format(time.RFC3339),
				IP:      c.ClientIP(),
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	apiAuthed.POST("/logout", func(c *gin.Context) {
		s.setSessionCookie(c, "", -1)
		reply(c, nil)
	})

	apiAuthed.GET("/me", func(c *gin.Context) {
		email := c.GetString("user")
		c.JSON(http.StatusOK, gin.H{"email": email, "root": c.GetBool("isRoot"), "sites": s.Sites(email)})
	})

	admin := apiAuthed.Group("", RequireRoot())
	admin.GET("/users", func(c *gin.Context) { c.JSON(http.StatusOK, s.Users()) })

	admin.POST("/users", func(c *gin.Context) {
		var body credentials
		if err := c.ShouldBindJSON(&body); err != nil {
			reply(c, err)
			return
		}
		reply(c, s.AddUser(body.Email, body.Password))
	})

	admin.PUT("/users/:email", func(c *gin.Context) {
		var body struct {
			Password *string   `json:"password"`
			Sites    *[]string `json:"sites"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			reply(c, err)
			return
		}
		reply(c, s.UpdateUser(c.Param("email"), body.Password, body.Sites))
	})

	admin.DELETE("/users/:email", func(c *gin.Context) {
		reply(c, s.DeleteUser(c.Param("email")))
	})

	admin.GET("/waiting-list", func(c *gin.Context) {
		s.wlMu.Lock()
		entries := s.waitlist()
		s.wlMu.Unlock()
		if entries == nil {
			entries = []WaitlistEntry{} // liste vide ⇒ [] et non null (le client itère dessus)
		}
		c.JSON(http.StatusOK, entries)
	})
}
