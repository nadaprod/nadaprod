package images

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// capture : dernière requête reçue par le faux serveur. Les assertions se
// font dans la goroutine du test, pas dans le handler — d'où le verrou.
type capture struct {
	mu   sync.Mutex
	last request
	hits int
}

type request struct {
	path   string
	query  url.Values
	header http.Header
}

func (r request) param(name string) string { return r.query.Get(name) }

func (c *capture) get() request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

func stubServer(t *testing.T, status int, body string, header map[string]string) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.last = request{path: r.URL.Path, query: r.URL.Query(), header: r.Header.Clone()}
		c.hits++
		c.mu.Unlock()
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

const pexelsBody = `{"page":1,"per_page":8,"total_results":2,"photos":[
 {"id":123,"width":4000,"height":2667,"url":"https://www.pexels.com/photo/team-123/",
  "photographer":"Jane Doe","photographer_url":"https://www.pexels.com/@jane","alt":"People in a meeting",
  "src":{"original":"https://images.pexels.com/photos/123/o.jpeg",
         "large2x":"https://images.pexels.com/photos/123/p.jpeg?auto=compress&cs=tinysrgb&dpr=2&h=650&w=940",
         "large":"https://images.pexels.com/photos/123/p.jpeg?auto=compress&h=650&w=940",
         "medium":"https://images.pexels.com/photos/123/p.jpeg?auto=compress&h=350",
         "landscape":"https://images.pexels.com/photos/123/l.jpeg","portrait":"https://images.pexels.com/photos/123/pt.jpeg"}},
 {"id":456,"width":3000,"height":4500,"url":"https://www.pexels.com/photo/desk-456/",
  "photographer":"John Roe","photographer_url":"https://www.pexels.com/@john","alt":"",
  "src":{"large":"https://images.pexels.com/photos/456/large.jpeg","medium":"https://images.pexels.com/photos/456/medium.jpeg"}},
 {"id":789,"width":10,"height":10,"url":"https://www.pexels.com/photo/broken-789/","photographer":"Nobody","src":{}}
]}`

func TestPexelsSearch(t *testing.T) {
	srv, reqs := stubServer(t, http.StatusOK, pexelsBody, nil)
	p := NewPexels("  PEXELS-KEY ", srv.URL+"/", nil)
	if p.Name() != "pexels" {
		t.Fatalf("Name() = %q", p.Name())
	}
	photos, err := p.Search(context.Background(), "  Startup   TEAM ", "landscape", 8)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	got := reqs.get()
	if got.path != "/v1/search" {
		t.Errorf("path = %q, attendu /v1/search", got.path)
	}
	if h := got.header.Get("Authorization"); h != "PEXELS-KEY" {
		t.Errorf("Authorization = %q, attendu la clé nue (sans Bearer)", h)
	}
	if q := got.param("query"); q != "startup team" {
		t.Errorf("query = %q", q)
	}
	if n := got.param("per_page"); n != "8" {
		t.Errorf("per_page = %q", n)
	}
	if o := got.param("orientation"); o != "landscape" {
		t.Errorf("orientation = %q", o)
	}

	// La 3e photo n'a aucune URL exploitable : elle est écartée.
	if len(photos) != 2 {
		t.Fatalf("len(photos) = %d, attendu 2", len(photos))
	}
	want := Photo{
		ID:        "123",
		Provider:  "pexels",
		URL:       "https://images.pexels.com/photos/123/p.jpeg?auto=compress&cs=tinysrgb&dpr=2&h=650&w=940",
		Thumb:     "https://images.pexels.com/photos/123/p.jpeg?auto=compress&h=350",
		Width:     4000,
		Height:    2667,
		Alt:       "People in a meeting",
		Author:    "Jane Doe",
		AuthorURL: "https://www.pexels.com/@jane",
		PageURL:   "https://www.pexels.com/photo/team-123/",
	}
	if photos[0] != want {
		t.Errorf("photos[0] =\n %+v\nattendu\n %+v", photos[0], want)
	}
	// Pas de large2x ⇒ repli sur large.
	if photos[1].URL != "https://images.pexels.com/photos/456/large.jpeg" {
		t.Errorf("repli large : URL = %q", photos[1].URL)
	}
	if photos[1].ID != "456" || photos[1].Thumb != "https://images.pexels.com/photos/456/medium.jpeg" {
		t.Errorf("photos[1] = %+v", photos[1])
	}
}

func TestPexelsOrientationAndPerPage(t *testing.T) {
	cases := []struct {
		orientation     string
		perPage         int
		wantOrientation string // "" ⇒ paramètre absent
		wantPerPage     string
	}{
		{"", 8, "", "8"},
		{"portrait", 100, "portrait", "30"},
		{"square", 0, "square", "1"},
		{"squarish", -5, "square", "1"},
		{"diagonal", 30, "", "30"},
		{" Landscape ", 1, "landscape", "1"},
	}
	for _, tc := range cases {
		srv, reqs := stubServer(t, http.StatusOK, `{"photos":[]}`, nil)
		if _, err := NewPexels("k", srv.URL, nil).Search(context.Background(), "cat", tc.orientation, tc.perPage); err != nil {
			t.Fatalf("%+v : %v", tc, err)
		}
		got := reqs.get()
		_, present := got.query["orientation"]
		if present != (tc.wantOrientation != "") || got.param("orientation") != tc.wantOrientation {
			t.Errorf("%+v : orientation = %q (présent=%v)", tc, got.param("orientation"), present)
		}
		if got.param("per_page") != tc.wantPerPage {
			t.Errorf("%+v : per_page = %q", tc, got.param("per_page"))
		}
	}
}

const unsplashBody = `{"total":2,"total_pages":1,"results":[
 {"id":"abc123","width":6000,"height":4000,"alt_description":"a group of people around a table","description":"ignored",
  "urls":{"raw":"https://images.unsplash.com/photo-1?ixid=M3w&ixlib=rb-4.0.3",
          "regular":"https://images.unsplash.com/photo-1?ixid=M3w&w=1080","small":"https://images.unsplash.com/photo-1?ixid=M3w&w=400"},
  "links":{"html":"https://unsplash.com/photos/abc123"},
  "user":{"name":"Ana Silva","links":{"html":"https://unsplash.com/@ana"}}},
 {"id":"def456","width":3000,"height":3000,"alt_description":null,"description":"Office desk",
  "urls":{"raw":"https://images.unsplash.com/photo-2","regular":"https://images.unsplash.com/photo-2-regular","small":"https://images.unsplash.com/photo-2-small"},
  "links":{"html":"https://unsplash.com/photos/def456?foo=1"},
  "user":{"name":"Bob","links":{"html":""}}}
]}`

func TestUnsplashSearch(t *testing.T) {
	srv, reqs := stubServer(t, http.StatusOK, unsplashBody, nil)
	u := NewUnsplash("UNSPLASH-KEY", srv.URL, nil)
	if u.Name() != "unsplash" {
		t.Fatalf("Name() = %q", u.Name())
	}
	photos, err := u.Search(context.Background(), "Team Meeting", "square", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	got := reqs.get()
	if got.path != "/search/photos" {
		t.Errorf("path = %q, attendu /search/photos", got.path)
	}
	if h := got.header.Get("Authorization"); h != "Client-ID UNSPLASH-KEY" {
		t.Errorf("Authorization = %q", h)
	}
	if h := got.header.Get("Accept-Version"); h != "v1" {
		t.Errorf("Accept-Version = %q", h)
	}
	if q := got.param("query"); q != "team meeting" {
		t.Errorf("query = %q", q)
	}
	if n := got.param("per_page"); n != "30" {
		t.Errorf("per_page = %q, attendu 30 (borne haute)", n)
	}
	if o := got.param("orientation"); o != "squarish" {
		t.Errorf("orientation = %q, attendu squarish", o)
	}

	if len(photos) != 2 {
		t.Fatalf("len(photos) = %d", len(photos))
	}
	want := Photo{
		ID:        "abc123",
		Provider:  "unsplash",
		URL:       "https://images.unsplash.com/photo-1?ixid=M3w&ixlib=rb-4.0.3&w=1600&q=80&auto=format&fit=crop",
		Thumb:     "https://images.unsplash.com/photo-1?ixid=M3w&w=400",
		Width:     6000,
		Height:    4000,
		Alt:       "a group of people around a table",
		Author:    "Ana Silva",
		AuthorURL: "https://unsplash.com/@ana?utm_source=nadaprod&utm_medium=referral",
		PageURL:   "https://unsplash.com/photos/abc123?utm_source=nadaprod&utm_medium=referral",
	}
	if photos[0] != want {
		t.Errorf("photos[0] =\n %+v\nattendu\n %+v", photos[0], want)
	}
	// raw sans « ? » ⇒ regular ; alt_description null ⇒ repli sur description ;
	// lien déjà pourvu d'une query ⇒ « & » ; lien vide ⇒ reste vide.
	p := photos[1]
	if p.URL != "https://images.unsplash.com/photo-2-regular" {
		t.Errorf("URL = %q, attendu regular", p.URL)
	}
	if p.Alt != "Office desk" {
		t.Errorf("Alt = %q", p.Alt)
	}
	if p.PageURL != "https://unsplash.com/photos/def456?foo=1&utm_source=nadaprod&utm_medium=referral" {
		t.Errorf("PageURL = %q", p.PageURL)
	}
	if p.AuthorURL != "" {
		t.Errorf("AuthorURL = %q, attendu vide", p.AuthorURL)
	}
}

func TestUnsplashOrientation(t *testing.T) {
	for in, want := range map[string]string{"": "", "landscape": "landscape", "portrait": "portrait", "squarish": "squarish", "weird": ""} {
		srv, reqs := stubServer(t, http.StatusOK, `{"results":[]}`, nil)
		if _, err := NewUnsplash("k", srv.URL, nil).Search(context.Background(), "cat", in, 8); err != nil {
			t.Fatalf("%q : %v", in, err)
		}
		got := reqs.get()
		_, present := got.query["orientation"]
		if present != (want != "") || got.param("orientation") != want {
			t.Errorf("orientation %q ⇒ %q (présent=%v), attendu %q", in, got.param("orientation"), present, want)
		}
	}
}

func TestProviderErrorStatuses(t *testing.T) {
	providers := map[string]func(base string) Provider{
		"pexels":   func(base string) Provider { return NewPexels("k", base, nil) },
		"unsplash": func(base string) Provider { return NewUnsplash("k", base, nil) },
	}
	cases := []struct {
		status  int
		body    string
		header  map[string]string
		want    string
		notWant string
		fatal   bool
	}{
		{401, `{"error":"bad key"}`, nil, "clé API refusée", "quota", true},
		{403, `{"error":"forbidden"}`, nil, "clé API refusée", "quota", true},
		{429, `Too Many Requests`, nil, "quota atteint", "clé", true},
		{403, `Rate Limit Exceeded`, nil, "quota atteint", "clé", true}, // quota façon Unsplash
		{403, ``, map[string]string{"X-Ratelimit-Remaining": "0"}, "quota atteint", "clé", true},
		{500, `boom`, nil, "500", "quota", false},
		{404, ``, nil, "404", "clé", false},
	}
	for name, mk := range providers {
		for _, tc := range cases {
			srv, _ := stubServer(t, tc.status, tc.body, tc.header)
			photos, err := mk(srv.URL).Search(context.Background(), "cat", "", 8)
			if err == nil {
				t.Fatalf("%s %d : erreur attendue, reçu %d photos", name, tc.status, len(photos))
			}
			msg := err.Error()
			if !strings.Contains(msg, name) {
				t.Errorf("%s %d : le message ne cite pas le fournisseur : %q", name, tc.status, msg)
			}
			if !strings.Contains(msg, tc.want) {
				t.Errorf("%s %d : %q ne contient pas %q", name, tc.status, msg, tc.want)
			}
			if tc.notWant != "" && strings.Contains(msg, tc.notWant) {
				t.Errorf("%s %d : %q ne devrait pas contenir %q", name, tc.status, msg, tc.notWant)
			}
			if isFatal(err) != tc.fatal {
				t.Errorf("%s %d : isFatal = %v, attendu %v", name, tc.status, isFatal(err), tc.fatal)
			}
		}
	}
}

func TestProviderBadJSONAndEmptyQuery(t *testing.T) {
	srv, reqs := stubServer(t, http.StatusOK, `<html>not json</html>`, nil)
	for _, p := range []Provider{NewPexels("k", srv.URL, nil), NewUnsplash("k", srv.URL, nil)} {
		if _, err := p.Search(context.Background(), "cat", "", 8); err == nil || !strings.Contains(err.Error(), "illisible") {
			t.Errorf("%s : JSON invalide ⇒ err = %v", p.Name(), err)
		}
		before := reqs.count()
		if _, err := p.Search(context.Background(), "  \n ", "", 8); err == nil {
			t.Errorf("%s : requête vide acceptée", p.Name())
		}
		if reqs.count() != before {
			t.Errorf("%s : une requête vide ne doit pas partir sur le réseau", p.Name())
		}
	}
}

func TestProviderContextCancelled(t *testing.T) {
	srv, _ := stubServer(t, http.StatusOK, `{"photos":[],"results":[]}`, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, p := range []Provider{NewPexels("k", srv.URL, nil), NewUnsplash("k", srv.URL, nil)} {
		if _, err := p.Search(ctx, "cat", "", 8); err == nil {
			t.Errorf("%s : contexte annulé ⇒ erreur attendue", p.Name())
		}
	}
}

// Valeurs par défaut, vérifiées sans toucher au réseau.
func TestProviderDefaults(t *testing.T) {
	px := NewPexels("k", "", nil).(*pexels)
	if px.base != "https://api.pexels.com" || px.http.Timeout != 10*time.Second {
		t.Errorf("pexels : base=%q timeout=%v", px.base, px.http.Timeout)
	}
	un := NewUnsplash("k", "", nil).(*unsplash)
	if un.base != "https://api.unsplash.com" || un.http.Timeout != 10*time.Second {
		t.Errorf("unsplash : base=%q timeout=%v", un.base, un.http.Timeout)
	}
	custom := &http.Client{Timeout: time.Second}
	if NewPexels("k", "", custom).(*pexels).http != custom {
		t.Error("le client fourni doit être utilisé tel quel")
	}
}
