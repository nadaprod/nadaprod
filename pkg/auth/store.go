package auth

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"log"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Package auth — comptes, sessions et liste d'attente.
//
// Pas d'inscription publique : root (ROOT_EMAIL / ROOT_PASSWORD, jamais stocké
// dans le fichier) crée les comptes et leur attribue des sites. Les candidats
// passent par /waiting-list. Le filesystem reste la base de données :
//
//	users.json        → { "ami@ex.com": { "hash": "$2a$…", "sites": ["pp"] } }
//	waiting-list.json → [ { "name", "email", "message", "date", "ip" }, … ]

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type UserRecord struct {
	Hash  string   `json:"hash"`
	Sites []string `json:"sites"`
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

var errUnknownUser = errors.New("compte inconnu")

// hashPassword applique la règle de longueur commune puis hache (bcrypt).
func hashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("mot de passe trop court (8 caractères minimum)")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

// save écrit users.json ; à appeler sous s.mu (écriture).
func (s *UserStore) save() error {
	b, _ := json.MarshalIndent(s.users, "", "  ")
	return os.WriteFile(s.path, b, 0o600)
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
	return ok && slices.Contains(u.Sites, siteID)
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
		return errors.New("email invalide")
	}
	if email == s.rootEmail {
		return errors.New("cet email est le compte administrateur")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[email]; ok {
		return errors.New("ce compte existe déjà")
	}
	s.users[email] = &UserRecord{Hash: hash, Sites: []string{}}
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
		return errUnknownUser
	}
	if password != nil {
		hash, err := hashPassword(*password)
		if err != nil {
			return err
		}
		u.Hash = hash
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
		return errUnknownUser
	}
	delete(s.users, email)
	return s.save()
}

func (s *UserStore) Users() []gin.H {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]gin.H, 0, len(s.users))
	for _, e := range slices.Sorted(maps.Keys(s.users)) {
		out = append(out, gin.H{"email": e, "sites": s.users[e].Sites})
	}
	return out
}
