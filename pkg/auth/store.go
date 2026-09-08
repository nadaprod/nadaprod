package auth

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"log"
	"os"
	"regexp"
	"sort"
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
