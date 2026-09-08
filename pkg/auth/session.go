package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Session = cookie signé HMAC (aucune dépendance de session) :
//	np_session = base64url(email) . expiration-unix . hex(hmac-sha256(secret, payload))

const (
	sessionCookie = "np_session"
	sessionTTL    = 30 * 24 * time.Hour
)

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
