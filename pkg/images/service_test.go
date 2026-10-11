package images

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProvider : fournisseur en mémoire qui journalise ses appels.
type fakeProvider struct {
	mu    sync.Mutex
	calls []fakeCall
	fn    func(query, orientation string, perPage int) ([]Photo, error)
}

type fakeCall struct {
	query, orientation string
	perPage            int
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Search(ctx context.Context, query, orientation string, perPage int) ([]Photo, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{query, orientation, perPage})
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.fn(query, orientation, perPage)
}

func (f *fakeProvider) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeProvider) log() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCall(nil), f.calls...)
}

// manyPhotos : perPage photos distinctes et reconnaissables par requête.
func manyPhotos(query, _ string, perPage int) ([]Photo, error) {
	slug := strings.ReplaceAll(query, " ", "-")
	out := make([]Photo, perPage)
	for i := range out {
		out[i] = Photo{
			ID:       fmt.Sprintf("%s-%d", slug, i),
			Provider: "fake",
			URL:      fmt.Sprintf("https://img.test/%s/%d.jpg", slug, i),
			Author:   "Jane Doe",
		}
	}
	return out, nil
}

// fakeClock : horloge manuelle, sûre en concurrence.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestServiceCacheHitAvoidsSecondHTTPCall(t *testing.T) {
	srv, reqs := stubServer(t, http.StatusOK, pexelsBody, nil)
	s := NewService(NewPexels("k", srv.URL, nil))
	ctx := context.Background()

	first, err := s.Search(ctx, "startup team", "landscape", 8)
	if err != nil {
		t.Fatal(err)
	}
	// Même requête à la normalisation près ⇒ même entrée de cache.
	second, err := s.Search(ctx, "  Startup\n TEAM  ", " Landscape", 8)
	if err != nil {
		t.Fatal(err)
	}
	if reqs.count() != 1 {
		t.Fatalf("appels HTTP = %d, attendu 1 (hit de cache)", reqs.count())
	}
	if len(first) != 2 || len(second) != 2 || first[0] != second[0] {
		t.Errorf("résultats incohérents : %v / %v", first, second)
	}

	// L'orientation fait partie de la clé.
	if _, err := s.Search(ctx, "startup team", "portrait", 8); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(ctx, "startup team", "", 8); err != nil {
		t.Fatal(err)
	}
	if reqs.count() != 3 {
		t.Errorf("appels HTTP = %d, attendu 3 (une par orientation)", reqs.count())
	}
}

func TestServiceCachePerPage(t *testing.T) {
	f := &fakeProvider{fn: manyPhotos}
	s := NewService(f)
	ctx := context.Background()

	if got, _ := s.Search(ctx, "cat", "", 8); len(got) != 8 {
		t.Fatalf("len = %d, attendu 8", len(got))
	}
	// Moins que ce qui est en cache ⇒ servi par le cache, tronqué.
	if got, _ := s.Search(ctx, "cat", "", 3); len(got) != 3 || f.count() != 1 {
		t.Errorf("perPage 3 : len=%d appels=%d, attendu 3 et 1", len(got), f.count())
	}
	// Plus que ce qui est en cache ⇒ nouvelle requête.
	if got, _ := s.Search(ctx, "cat", "", 20); len(got) != 20 || f.count() != 2 {
		t.Errorf("perPage 20 : len=%d appels=%d, attendu 20 et 2", len(got), f.count())
	}
	// perPage est borné avant d'atteindre le fournisseur.
	s.Search(ctx, "dog", "", 999)
	s.Search(ctx, "bird", "", -1)
	log := f.log()
	if log[2].perPage != 30 || log[3].perPage != 1 {
		t.Errorf("bornes perPage : %d et %d, attendu 30 et 1", log[2].perPage, log[3].perPage)
	}

	// Le fournisseur a renvoyé moins que demandé : il n'a rien de plus, inutile
	// de le rappeler pour un perPage supérieur.
	few := &fakeProvider{fn: func(q, o string, n int) ([]Photo, error) { return manyPhotos(q, o, 2) }}
	s = NewService(few)
	s.Search(ctx, "rare", "", 8)
	if got, _ := s.Search(ctx, "rare", "", 30); len(got) != 2 || few.count() != 1 {
		t.Errorf("résultats épuisés : len=%d appels=%d, attendu 2 et 1", len(got), few.count())
	}
}

func TestServiceCacheExpiry(t *testing.T) {
	f := &fakeProvider{fn: manyPhotos}
	clock := newFakeClock()
	s := NewService(f)
	s.now = clock.now
	ctx := context.Background()

	s.Search(ctx, "cat", "", 8)
	clock.advance(24*time.Hour - time.Second)
	s.Search(ctx, "cat", "", 8)
	if f.count() != 1 {
		t.Fatalf("avant 24 h : appels = %d, attendu 1", f.count())
	}
	clock.advance(time.Second)
	s.Search(ctx, "cat", "", 8)
	if f.count() != 2 {
		t.Fatalf("à 24 h : appels = %d, attendu 2 (entrée expirée)", f.count())
	}
	// L'entrée rafraîchie repart pour 24 h.
	clock.advance(23 * time.Hour)
	s.Search(ctx, "cat", "", 8)
	if f.count() != 2 {
		t.Errorf("après rafraîchissement : appels = %d, attendu 2", f.count())
	}
}

func TestServiceCacheEviction(t *testing.T) {
	f := &fakeProvider{fn: manyPhotos}
	clock := newFakeClock()
	s := NewService(f)
	s.now = clock.now
	ctx := context.Background()

	if s.maxEntries != 500 || s.ttl != 24*time.Hour {
		t.Fatalf("défauts : maxEntries=%d ttl=%v", s.maxEntries, s.ttl)
	}
	for i := range 501 {
		if _, err := s.Search(ctx, fmt.Sprintf("query %d", i), "", 8); err != nil {
			t.Fatal(err)
		}
		clock.advance(time.Second)
	}
	if n := len(s.cache); n != 500 {
		t.Fatalf("taille du cache = %d, attendu 500", n)
	}
	if _, ok := s.cache["query 0|"]; ok {
		t.Error("la plus ancienne entrée aurait dû être évincée")
	}
	if _, ok := s.cache["query 1|"]; !ok {
		t.Error("la 2e entrée ne devait pas être évincée")
	}

	calls := f.count()
	s.Search(ctx, "query 500", "", 8) // la plus récente : toujours en cache
	if f.count() != calls {
		t.Error("la plus récente entrée aurait dû rester en cache")
	}
	s.Search(ctx, "query 0", "", 8) // évincée : repart chez le fournisseur
	if f.count() != calls+1 {
		t.Error("l'entrée évincée aurait dû être redemandée")
	}
	if n := len(s.cache); n != 500 {
		t.Errorf("taille du cache après réinsertion = %d, attendu 500", n)
	}
}

// Cache plein : les entrées expirées partent avant les entrées fraîches.
func TestServiceEvictionPrefersExpired(t *testing.T) {
	f := &fakeProvider{fn: manyPhotos}
	clock := newFakeClock()
	s := NewService(f)
	s.now = clock.now
	s.maxEntries = 3
	ctx := context.Background()

	s.Search(ctx, "a", "", 8)
	s.Search(ctx, "b", "", 8)
	clock.advance(25 * time.Hour)
	s.Search(ctx, "c", "", 8)
	s.Search(ctx, "d", "", 8) // plein : a et b (expirées) partent, c reste
	if _, ok := s.cache["c|"]; !ok || len(s.cache) != 2 {
		t.Errorf("cache = %d entrées, c présent = %v ; attendu c et d seuls", len(s.cache), ok)
	}
}

func TestServiceErrorsNotCachedAndCopies(t *testing.T) {
	fail := true
	f := &fakeProvider{fn: func(q, o string, n int) ([]Photo, error) {
		if fail {
			return nil, errors.New("panne")
		}
		return manyPhotos(q, o, n)
	}}
	s := NewService(f)
	ctx := context.Background()

	if _, err := s.Search(ctx, "cat", "", 8); err == nil {
		t.Fatal("erreur attendue")
	}
	fail = false
	got, err := s.Search(ctx, "cat", "", 8)
	if err != nil || f.count() != 2 {
		t.Fatalf("une erreur ne doit pas être mise en cache : err=%v appels=%d", err, f.count())
	}
	// Modifier le résultat ne doit pas corrompre le cache.
	got[0].URL = "https://evil.test/x.jpg"
	again, _ := s.Search(ctx, "cat", "", 8)
	if again[0].URL != "https://img.test/cat/0.jpg" {
		t.Errorf("le cache a été modifié par l'appelant : %q", again[0].URL)
	}

	if _, err := s.Search(ctx, "   ", "", 8); err == nil {
		t.Error("requête vide acceptée")
	}
	if f.count() != 2 {
		t.Error("une requête vide ne doit pas atteindre le fournisseur")
	}
}

func TestServiceConcurrentSearch(t *testing.T) {
	f := &fakeProvider{fn: manyPhotos}
	s := NewService(f)
	s.maxEntries = 10 // force des évictions concurrentes
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			for i := range 50 {
				if _, err := s.Search(context.Background(), fmt.Sprintf("q %d", (g+i)%25), "", 8); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if n := len(s.cache); n > 10 {
		t.Errorf("taille du cache = %d, borne = 10", n)
	}
}

func TestServiceDisabled(t *testing.T) {
	var nilService *Service
	for name, s := range map[string]*Service{"NewService(nil)": NewService(nil), "nil": nilService} {
		if s.Enabled() {
			t.Errorf("%s : Enabled() = true", name)
		}
		if s.ProviderName() != "" {
			t.Errorf("%s : ProviderName() = %q", name, s.ProviderName())
		}
		if _, err := s.Search(context.Background(), "cat", "", 8); !errors.Is(err, ErrDisabled) {
			t.Errorf("%s : Search err = %v, attendu ErrDisabled", name, err)
		}
	}
}

func TestFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name string
		vars map[string]string
		want string // "" ⇒ désactivé
	}{
		{"aucune clé", nil, ""},
		{"clés blanches", map[string]string{"PEXELS_API_KEY": "  ", "UNSPLASH_ACCESS_KEY": "\t"}, ""},
		{"pexels seul", map[string]string{"PEXELS_API_KEY": "p"}, "pexels"},
		{"unsplash seul", map[string]string{"UNSPLASH_ACCESS_KEY": "u"}, "unsplash"},
		{"les deux ⇒ pexels", map[string]string{"PEXELS_API_KEY": "p", "UNSPLASH_ACCESS_KEY": "u"}, "pexels"},
		{"pexels blanche ⇒ unsplash", map[string]string{"PEXELS_API_KEY": " ", "UNSPLASH_ACCESS_KEY": "u"}, "unsplash"},
	}
	for _, tc := range cases {
		s := FromEnv(env(tc.vars))
		if s == nil {
			t.Fatalf("%s : FromEnv ne doit jamais renvoyer nil", tc.name)
		}
		if s.ProviderName() != tc.want || s.Enabled() != (tc.want != "") {
			t.Errorf("%s : provider=%q enabled=%v, attendu %q", tc.name, s.ProviderName(), s.Enabled(), tc.want)
		}
	}
	// La clé est transmise (espaces retirés) au fournisseur choisi.
	s := FromEnv(env(map[string]string{"UNSPLASH_ACCESS_KEY": " abc "}))
	if u, ok := s.provider.(*unsplash); !ok || u.key != "abc" || u.base != unsplashBaseURL {
		t.Errorf("fournisseur inattendu : %#v", s.provider)
	}
}

func TestSanitizeQuery(t *testing.T) {
	long := strings.Repeat("abcdefghi ", 12) // 120 caractères
	cases := map[string]string{
		"  Startup   TEAM\tmeeting \n": "startup team meeting",
		"":                             "",
		" \n\t ":                       "",
		"café\x00terrasse":             "café terrasse",
		"ÉQUIPE Créative":              "équipe créative",
		long:                           strings.TrimSpace(long[:80]),
	}
	for in, want := range cases {
		if got := sanitizeQuery(in); got != want {
			t.Errorf("sanitizeQuery(%q) = %q, attendu %q", in, got, want)
		}
	}
	if n := len([]rune(sanitizeQuery(strings.Repeat("é", 200)))); n != 80 {
		t.Errorf("troncature en caractères, pas en octets : %d", n)
	}
}
