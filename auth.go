package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// auth.go — comptes, sessions et liste d'attente.
//
// Pas d'inscription publique : root (ROOT_EMAIL / ROOT_PASSWORD, jamais stocké
// dans le fichier) crée les comptes et leur attribue des sites. Les candidats
// passent par /waiting-list. Le filesystem reste la base de données :
//
//	users.json        → { "ami@ex.com": { "hash": "$2a$…", "sites": ["pp"] } }
//	waiting-list.json → [ { "name", "email", "message", "date", "ip" }, … ]
//
// Session = cookie signé HMAC (aucune dépendance de session) :
//	np_session = base64url(email) . expiration-unix . hex(hmac-sha256(secret, payload))

const (
	sessionCookie   = "np_session"
	sessionTTL      = 30 * 24 * time.Hour
	maxWaitlist     = 5000 // au-delà, on accepte sans stocker (anti-abus)
	failedLoginWait = 300 * time.Millisecond
)

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type UserRecord struct {
	Hash  string   `json:"hash"`
	Sites []string `json:"sites"`
}

type WaitlistEntry struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`
	Date    string `json:"date"`
	IP      string `json:"ip"`
}

type UserStore struct {
	path      string // users.json
	wlPath    string // waiting-list.json
	rootEmail string
	rootHash  []byte
	secret    []byte
	mu        sync.RWMutex // users
	users     map[string]*UserRecord
	wlMu      sync.Mutex // liste d'attente
}

func NewUserStore(path, wlPath, rootEmail, rootPassword, secret string) (*UserStore, error) {
	rootHash, err := bcrypt.GenerateFromPassword([]byte(rootPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	sec := []byte(secret)
	if len(sec) == 0 {
		sec = make([]byte, 32)
		if _, err := rand.Read(sec); err != nil {
			return nil, err
		}
		log.Printf("AUTH_SECRET absent — secret aléatoire généré, les sessions expireront au prochain redémarrage")
	}
	s := &UserStore{
		path:      path,
		wlPath:    wlPath,
		rootEmail: normalizeEmail(rootEmail),
		rootHash:  rootHash,
		secret:    sec,
		users:     map[string]*UserRecord{},
	}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &s.users); err != nil {
			return nil, fmt.Errorf("%s illisible : %w", path, err)
		}
	}
	return s, nil
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// save écrit users.json ; à appeler sous s.mu (écriture).
func (s *UserStore) save() error {
	b, _ := json.MarshalIndent(s.users, "", "  ")
	return os.WriteFile(s.path, b, 0o600)
}

// ---------- Sessions (cookie signé) ----------

func (s *UserStore) sign(payload string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *UserStore) MakeSession(email string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(normalizeEmail(email))) +
		"." + strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)
	return payload + "." + s.sign(payload)
}

func (s *UserStore) ParseSession(v string) (string, bool) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return "", false
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(s.sign(payload)), []byte(parts[2])) {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	email, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	return string(email), true
}

// ---------- Comptes ----------

func (s *UserStore) IsRoot(email string) bool { return normalizeEmail(email) == s.rootEmail }

// exists : le compte est-il encore valide ? (un utilisateur supprimé garde un
// cookie signé valide, mais échoue ici → 401)
func (s *UserStore) exists(email string) bool {
	email = normalizeEmail(email)
	if email == s.rootEmail {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.users[email]
	return ok
}

func (s *UserStore) Authenticate(email, password string) bool {
	email = normalizeEmail(email)
	if email == s.rootEmail {
		return bcrypt.CompareHashAndPassword(s.rootHash, []byte(password)) == nil
	}
	s.mu.RLock()
	u, ok := s.users[email]
	s.mu.RUnlock()
	return ok && bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)) == nil
}

// CanAccess : root voit tout ; un utilisateur, uniquement ses sites.
func (s *UserStore) CanAccess(email, siteID string) bool {
	email = normalizeEmail(email)
	if email == s.rootEmail {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[email]
	if !ok {
		return false
	}
	for _, id := range u.Sites {
		if id == siteID {
			return true
		}
	}
	return false
}

// Sites renvoie les sites attribués (nil pour root : « tous »).
func (s *UserStore) Sites(email string) []string {
	email = normalizeEmail(email)
	if email == s.rootEmail {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.users[email]; ok {
		return append([]string{}, u.Sites...)
	}
	return []string{}
}

func (s *UserStore) AddUser(email, password string) error {
	email = normalizeEmail(email)
	if !emailRe.MatchString(email) {
		return fmt.Errorf("email invalide")
	}
	if email == s.rootEmail {
		return fmt.Errorf("cet email est le compte administrateur")
	}
	if len(password) < 8 {
		return fmt.Errorf("mot de passe trop court (8 caractères minimum)")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[email]; ok {
		return fmt.Errorf("ce compte existe déjà")
	}
	s.users[email] = &UserRecord{Hash: string(hash), Sites: []string{}}
	return s.save()
}

// UpdateUser : mise à jour partielle (même convention que /settings — un champ
// nil n'est pas modifié).
func (s *UserStore) UpdateUser(email string, password *string, sites *[]string) error {
	email = normalizeEmail(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[email]
	if !ok {
		return fmt.Errorf("compte inconnu")
	}
	if password != nil {
		if len(*password) < 8 {
			return fmt.Errorf("mot de passe trop court (8 caractères minimum)")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		u.Hash = string(hash)
	}
	if sites != nil {
		u.Sites = append([]string{}, *sites...)
	}
	return s.save()
}

func (s *UserStore) DeleteUser(email string) error {
	email = normalizeEmail(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[email]; !ok {
		return fmt.Errorf("compte inconnu")
	}
	delete(s.users, email)
	return s.save()
}

func (s *UserStore) Users() []gin.H {
	s.mu.RLock()
	defer s.mu.RUnlock()
	emails := make([]string, 0, len(s.users))
	for e := range s.users {
		emails = append(emails, e)
	}
	sort.Strings(emails)
	out := make([]gin.H, 0, len(emails))
	for _, e := range emails {
		out = append(out, gin.H{"email": e, "sites": s.users[e].Sites})
	}
	return out
}

// ---------- Liste d'attente ----------

func (s *UserStore) waitlist() []WaitlistEntry {
	var entries []WaitlistEntry
	if b, err := os.ReadFile(s.wlPath); err == nil {
		_ = json.Unmarshal(b, &entries)
	}
	return entries
}

// AddToWaitlist ajoute une candidature (dédupliquée par email).
func (s *UserStore) AddToWaitlist(e WaitlistEntry) error {
	s.wlMu.Lock()
	defer s.wlMu.Unlock()
	entries := s.waitlist()
	if len(entries) >= maxWaitlist {
		return nil // accepté silencieusement : le fichier ne grossit plus
	}
	for _, old := range entries {
		if old.Email == e.Email {
			return nil // déjà inscrit : idempotent
		}
	}
	entries = append(entries, e)
	b, _ := json.MarshalIndent(entries, "", "  ")
	return os.WriteFile(s.wlPath, b, 0o600)
}

// ---------- Middlewares ----------

// RequireAuth : session valide et compte existant, sinon 401 (API) ou
// redirection vers /login (pages).
func (s *UserStore) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if v, err := c.Cookie(sessionCookie); err == nil {
			if email, ok := s.ParseSession(v); ok && s.exists(email) {
				c.Set("user", email)
				c.Set("isRoot", s.IsRoot(email))
				c.Next()
				return
			}
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentification requise"})
			return
		}
		c.Redirect(http.StatusFound, "/login?next="+url.QueryEscape(c.Request.URL.RequestURI()))
		c.Abort()
	}
}

// RequireRoot : à empiler après RequireAuth sur les routes d'administration.
func RequireRoot() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !c.GetBool("isRoot") {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "réservé à l'administrateur"})
		}
	}
}

// ---------- Routes ----------

func (s *UserStore) setSessionCookie(c *gin.Context, value string, maxAge int) {
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookie, value, maxAge, "/", "", secure, true)
}

// RegisterAuthRoutes : public = login + liste d'attente ; authed = le reste.
func (s *UserStore) RegisterAuthRoutes(r *gin.Engine, api, apiAuthed *gin.RouterGroup) {
	api.POST("/login", func(c *gin.Context) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	apiAuthed.GET("/me", func(c *gin.Context) {
		email := c.GetString("user")
		c.JSON(http.StatusOK, gin.H{"email": email, "root": c.GetBool("isRoot"), "sites": s.Sites(email)})
	})

	admin := apiAuthed.Group("", RequireRoot())
	admin.GET("/users", func(c *gin.Context) { c.JSON(http.StatusOK, s.Users()) })

	admin.POST("/users", func(c *gin.Context) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := s.AddUser(body.Email, body.Password); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	admin.PUT("/users/:email", func(c *gin.Context) {
		var body struct {
			Password *string   `json:"password"`
			Sites    *[]string `json:"sites"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := s.UpdateUser(c.Param("email"), body.Password, body.Sites); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	admin.DELETE("/users/:email", func(c *gin.Context) {
		if err := s.DeleteUser(c.Param("email")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	admin.GET("/waiting-list", func(c *gin.Context) {
		s.wlMu.Lock()
		entries := s.waitlist()
		s.wlMu.Unlock()
		c.JSON(http.StatusOK, entries)
	})
}
