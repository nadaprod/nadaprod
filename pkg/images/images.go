// Package images remplace les placeholders des blocs générés par de vraies
// photos de banques d'images (Pexels, Unsplash). Bibliothèque standard
// uniquement : le paquet ne dépend ni de gin ni du reste de l'application.
package images

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Photo : un résultat de recherche normalisé, quel que soit le fournisseur.
type Photo struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"` // "pexels" | "unsplash"
	URL       string `json:"url"`      // image large (~1600 px) utilisable en <img src>
	Thumb     string `json:"thumb"`    // vignette (~400 px)
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Alt       string `json:"alt"`
	Author    string `json:"author"`
	AuthorURL string `json:"author_url"`
	PageURL   string `json:"page_url"` // page de la photo chez le fournisseur (attribution)
}

// Provider : une banque d'images.
type Provider interface {
	Name() string
	Search(ctx context.Context, query string, orientation string, perPage int) ([]Photo, error)
}

// ErrDisabled : aucune clé configurée. Exporté pour que les appelants puissent
// distinguer « pas de banque d'images » d'une vraie panne (errors.Is).
var ErrDisabled = errors.New("banque d'images non configurée")

const (
	cacheTTL        = 24 * time.Hour
	cacheMaxEntries = 500
	maxPerPage      = 30
	maxQueryLen     = 80
)

// Service : fournisseur + cache mémoire (clé = requête normalisée +
// orientation), TTL 24 h, 500 entrées max, sûr en concurrence. Les quotas des
// API gratuites sont bas (50 à 200 requêtes/heure) : sans cache, une seule
// page régénérée plusieurs fois les épuiserait.
type Service struct {
	provider Provider

	mu    sync.Mutex
	cache map[string]cacheEntry

	// Injectables par les tests (horloge figée, expiration, petit cache).
	now        func() time.Time
	ttl        time.Duration
	maxEntries int
}

type cacheEntry struct {
	photos  []Photo
	perPage int // taille demandée au fournisseur : un hit doit la couvrir
	at      time.Time
}

// NewService : p == nil ⇒ service désactivé (Resolve devient un no-op).
func NewService(p Provider) *Service {
	return &Service{
		provider:   p,
		cache:      make(map[string]cacheEntry),
		now:        time.Now,
		ttl:        cacheTTL,
		maxEntries: cacheMaxEntries,
	}
}

// FromEnv choisit le fournisseur selon les variables (PEXELS_API_KEY
// prioritaire, sinon UNSPLASH_ACCESS_KEY) via la fonction getenv fournie ;
// aucune clé ⇒ service désactivé. getenv nil ⇒ os.Getenv.
func FromEnv(getenv func(string) string) *Service {
	if getenv == nil {
		getenv = os.Getenv
	}
	if key := strings.TrimSpace(getenv("PEXELS_API_KEY")); key != "" {
		return NewService(NewPexels(key, "", nil))
	}
	if key := strings.TrimSpace(getenv("UNSPLASH_ACCESS_KEY")); key != "" {
		return NewService(NewUnsplash(key, "", nil))
	}
	return NewService(nil)
}

// Enabled tolère un récepteur nil : l'appelant n'a pas à tester deux choses.
func (s *Service) Enabled() bool { return s != nil && s.provider != nil }

// ProviderName : "" si désactivé.
func (s *Service) ProviderName() string {
	if !s.Enabled() {
		return ""
	}
	return s.provider.Name()
}

// Search interroge le fournisseur à travers le cache. Les erreurs ne sont pas
// mises en cache (une panne passagère ne doit pas durer 24 h) ; un résultat
// vide l'est (c'est une réponse valide, inutile de la redemander).
func (s *Service) Search(ctx context.Context, query, orientation string, perPage int) ([]Photo, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	q := sanitizeQuery(query)
	if q == "" {
		return nil, errors.New("recherche d'images : requête vide")
	}
	o := normalizeOrientation(orientation)
	n := clampPerPage(perPage)
	key := q + "|" + o

	if photos, ok := s.cached(key, n); ok {
		return photos, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Appel réseau hors verrou : deux recherches identiques simultanées
	// coûtent au pire deux requêtes, mais aucune ne bloque les autres clés.
	photos, err := s.provider.Search(ctx, q, o, n)
	if err != nil {
		return nil, err
	}
	s.store(key, photos, n)
	return head(photos, n), nil
}

// cached renvoie une copie des photos en cache si l'entrée est fraîche et
// couvre la taille demandée : soit elle a été remplie avec un perPage au moins
// égal, soit le fournisseur avait renvoyé moins que demandé (il n'a rien de plus).
func (s *Service) cached(key string, n int) ([]Photo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok {
		return nil, false
	}
	if s.now().Sub(e.at) >= s.ttl {
		delete(s.cache, key)
		return nil, false
	}
	if e.perPage < n && len(e.photos) >= e.perPage {
		return nil, false
	}
	return head(e.photos, n), true
}

func (s *Service) store(key string, photos []Photo, perPage int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if _, exists := s.cache[key]; !exists && len(s.cache) >= s.maxEntries {
		// D'abord les entrées expirées, puis les plus anciennes. Le parcours
		// complet reste négligeable à 500 entrées, et n'a lieu que cache plein.
		for k, e := range s.cache {
			if now.Sub(e.at) >= s.ttl {
				delete(s.cache, k)
			}
		}
		for len(s.cache) >= s.maxEntries {
			oldestKey, oldestAt, first := "", time.Time{}, true
			for k, e := range s.cache {
				if first || e.at.Before(oldestAt) {
					oldestKey, oldestAt, first = k, e.at, false
				}
			}
			delete(s.cache, oldestKey)
		}
	}
	s.cache[key] = cacheEntry{photos: head(photos, len(photos)), perPage: perPage, at: now}
}

// head copie les n premières photos : ni l'appelant ni le fournisseur ne
// doivent pouvoir modifier le contenu du cache après coup.
func head(photos []Photo, n int) []Photo {
	n = min(n, len(photos))
	out := make([]Photo, n)
	copy(out, photos[:n])
	return out
}

// sanitizeQuery normalise une requête : espaces réduits, minuscules, 80
// caractères max. Sert aussi de clé de cache — « Team  Meeting » et
// « team meeting » doivent partager la même entrée.
func sanitizeQuery(q string) string {
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, q)
	q = strings.Join(strings.Fields(strings.ToLower(q)), " ")
	if r := []rune(q); len(r) > maxQueryLen {
		q = strings.TrimSpace(string(r[:maxQueryLen]))
	}
	return q
}

// normalizeOrientation ramène l'orientation au vocabulaire commun
// (landscape | portrait | square) ; toute autre valeur ⇒ "" (pas de filtre).
func normalizeOrientation(o string) string {
	switch o = strings.ToLower(strings.TrimSpace(o)); o {
	case "landscape", "portrait", "square":
		return o
	case "squarish": // vocabulaire Unsplash
		return "square"
	}
	return ""
}

func clampPerPage(n int) int { return min(max(n, 1), maxPerPage) }
