package publish

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const netlifyToken = "tok-netlify-secret"

const netlifySiteJSON = `{
	"id": "site-123",
	"name": "brave-curie-123",
	"url": "http://brave-curie-123.netlify.app",
	"ssl_url": "https://brave-curie-123.netlify.app",
	"admin_url": "https://app.netlify.com/sites/brave-curie-123",
	"deploy_id": "dep-1"
}`

// fakeNetlify : POST /sites et POST /sites/{site}/deploys répondent respond ;
// GET /deploys/{id} déroule states puis reste sur le dernier.
type fakeNetlify struct {
	rec     recorder
	status  int
	respond string
	states  []string
	polls   atomic.Int32
	onPoll  func(n int) // appelé à chaque sondage (n à partir de 1), avant la réponse
}

func (f *fakeNetlify) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.rec.add(r)
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/deploys/") {
		n := int(f.polls.Add(1))
		if f.onPoll != nil {
			f.onPoll(n)
		}
		state := f.states[min(n, len(f.states))-1]
		switch state {
		case "502":
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, "<html><body>Bad Gateway</body></html>")
		case "429":
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"code":429,"message":"Too many requests"}`)
		case "error":
			io.WriteString(w, `{"id":"dep-1","state":"error","error_message":"Invalid zip file"}`)
		default:
			fmt.Fprintf(w, `{"id":"dep-1","state":%q}`, state)
		}
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
	}
	io.WriteString(w, f.respond)
}

func newFakeNetlify(t *testing.T, f *fakeNetlify) (*fakeNetlify, *Netlify) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &Netlify{Token: netlifyToken, BaseURL: srv.URL}
}

func TestNetlifyDeployNewSite(t *testing.T) {
	fastPolling(t, time.Millisecond, 5*time.Second)
	f, n := newFakeNetlify(t, &fakeNetlify{
		status:  http.StatusCreated,
		respond: netlifySiteJSON,
		states:  []string{"uploaded", "processing", "ready"},
	})

	res, err := n.Deploy(context.Background(), "", siteFiles())
	if err != nil {
		t.Fatalf("Deploy : %v", err)
	}
	want := Result{
		Provider: "netlify",
		URL:      "https://brave-curie-123.netlify.app",
		DeployID: "dep-1",
		Site:     "site-123",
		AdminURL: "https://app.netlify.com/sites/brave-curie-123",
	}
	if res != want {
		t.Errorf("Result = %+v\nattendu  %+v", res, want)
	}

	wantCalls := []string{"POST /sites", "GET /deploys/dep-1", "GET /deploys/dep-1", "GET /deploys/dep-1"}
	if got := f.rec.keys(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("appels = %v\nattendu  %v", got, wantCalls)
	}
	reqs := f.rec.all()
	for _, r := range reqs {
		if r.Auth != "Bearer "+netlifyToken {
			t.Errorf("%s : Authorization = %q", r.key(), r.Auth)
		}
	}
	if ct := reqs[0].ContentType; ct != "application/zip" {
		t.Errorf("Content-Type = %q, attendu application/zip", ct)
	}

	// Le corps est un zip valide : exactement les fichiers du site, triés.
	zr, err := zip.NewReader(bytes.NewReader(reqs[0].Body), int64(len(reqs[0].Body)))
	if err != nil {
		t.Fatalf("corps zip illisible : %v", err)
	}
	var names []string
	for _, zf := range zr.File {
		names = append(names, zf.Name)
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("ouverture %s : %v", zf.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		for _, src := range siteFiles() {
			if src.Path == zf.Name && !bytes.Equal(src.Data, data) {
				t.Errorf("%s : contenu altéré dans le zip", zf.Name)
			}
		}
	}
	if wantNames := []string{"assets/photo-1.jpg", "index.html", "styles.css"}; !reflect.DeepEqual(names, wantNames) {
		t.Errorf("entrées du zip = %v, attendu %v", names, wantNames)
	}
}

func TestNetlifyRedeploy(t *testing.T) {
	fastPolling(t, time.Millisecond, 5*time.Second)
	cases := []struct {
		name     string
		site     string
		respond  string
		wantPath string
		wantURL  string
		wantSite string
	}{
		{
			name:     "ssl_url prioritaire",
			site:     "brave-curie-123.netlify.app",
			respond:  `{"id":"dep-1","site_id":"site-123","ssl_url":"https://brave.netlify.app","url":"http://brave.netlify.app","deploy_ssl_url":"https://dep-1--brave.netlify.app","admin_url":"https://app.netlify.com/sites/brave","state":"uploaded"}`,
			wantPath: "/sites/brave-curie-123.netlify.app/deploys",
			wantURL:  "https://brave.netlify.app",
			wantSite: "site-123",
		},
		{
			name:     "repli sur url",
			site:     "site-123",
			respond:  `{"id":"dep-1","site_id":"site-123","url":"http://brave.netlify.app","deploy_ssl_url":"https://dep-1--brave.netlify.app"}`,
			wantPath: "/sites/site-123/deploys",
			wantURL:  "http://brave.netlify.app",
			wantSite: "site-123",
		},
		{
			name:     "repli sur deploy_ssl_url",
			site:     "site-123",
			respond:  `{"id":"dep-1","site_id":"site-123","deploy_ssl_url":"https://dep-1--brave.netlify.app"}`,
			wantPath: "/sites/site-123/deploys",
			wantURL:  "https://dep-1--brave.netlify.app",
			wantSite: "site-123",
		},
		{
			name:     "URL collée par l'utilisateur, site_id absent de la réponse",
			site:     " https://brave-curie-123.netlify.app/ ",
			respond:  `{"id":"dep-1","ssl_url":"https://brave.netlify.app"}`,
			wantPath: "/sites/brave-curie-123.netlify.app/deploys",
			wantURL:  "https://brave.netlify.app",
			wantSite: "brave-curie-123.netlify.app",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, n := newFakeNetlify(t, &fakeNetlify{respond: c.respond, states: []string{"ready"}})
			res, err := n.Deploy(context.Background(), c.site, siteFiles())
			if err != nil {
				t.Fatalf("Deploy : %v", err)
			}
			reqs := f.rec.all()
			if reqs[0].Method != http.MethodPost || reqs[0].Path != c.wantPath {
				t.Errorf("premier appel = %s, attendu POST %s", reqs[0].key(), c.wantPath)
			}
			if reqs[0].ContentType != "application/zip" || reqs[0].Auth != "Bearer "+netlifyToken {
				t.Errorf("en-têtes = %q / %q", reqs[0].ContentType, reqs[0].Auth)
			}
			if _, err := zip.NewReader(bytes.NewReader(reqs[0].Body), int64(len(reqs[0].Body))); err != nil {
				t.Errorf("corps zip illisible : %v", err)
			}
			if f.rec.count("GET /deploys/dep-1") != 1 {
				t.Errorf("appels = %v, attendu un seul sondage", f.rec.keys())
			}
			if res.URL != c.wantURL || res.Site != c.wantSite || res.DeployID != "dep-1" || res.Provider != "netlify" {
				t.Errorf("Result = %+v", res)
			}
		})
	}
}

func TestNetlifyDeployStateError(t *testing.T) {
	fastPolling(t, time.Millisecond, 5*time.Second)
	_, n := newFakeNetlify(t, &fakeNetlify{respond: netlifySiteJSON, states: []string{"processing", "error"}})

	res, err := n.Deploy(context.Background(), "", siteFiles())
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("erreur = %v, attendu *Error", err)
	}
	if pe.Provider != "netlify" || pe.Code != CodeUpstream || pe.Message != "Invalid zip file" {
		t.Errorf("Error = %+v", pe)
	}
	// Le site a bel et bien été créé : l'appelant doit pouvoir le réutiliser.
	if res.Site != "site-123" {
		t.Errorf("Result.Site = %q, attendu le site créé malgré l'échec", res.Site)
	}
}

func TestNetlifyHTTPErrors(t *testing.T) {
	fastPolling(t, time.Millisecond, 5*time.Second)
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode string
		wantMsg  string
	}{
		{"401", 401, `{"code":401,"message":"Access Denied"}`, CodeAuth, "Access Denied"},
		{"403", 403, `{"message":"Forbidden"}`, CodeAuth, "Forbidden"},
		{"404", 404, `{"code":404,"message":"Not Found"}`, CodeNotFound, "Not Found"},
		{"429", 429, `{"code":429,"message":"Too many requests"}`, CodeRateLimited, "Too many requests"},
		{"422 validation", 422, `{"errors":{"name":["must be unique"]}}`, CodeInvalid, "must be unique"},
		{"500 texte", 500, `boom`, CodeUpstream, "boom"},
		{"502 HTML", 502, `<html><body>Bad Gateway</body></html>`, CodeUpstream, "Bad Gateway"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, n := newFakeNetlify(t, &fakeNetlify{status: c.status, respond: c.body, states: []string{"ready"}})
			res, err := n.Deploy(context.Background(), "site-123", siteFiles())
			var pe *Error
			if !errors.As(err, &pe) {
				t.Fatalf("erreur = %v, attendu *Error", err)
			}
			if pe.Provider != "netlify" || pe.Status != c.status || pe.Code != c.wantCode {
				t.Errorf("Error = %+v, attendu statut %d code %q", pe, c.status, c.wantCode)
			}
			if !strings.Contains(pe.Message, c.wantMsg) {
				t.Errorf("Message = %q, attendu un fragment %q", pe.Message, c.wantMsg)
			}
			if strings.Contains(err.Error(), netlifyToken) {
				t.Errorf("le jeton apparaît dans l'erreur : %q", err)
			}
			if res != (Result{}) {
				t.Errorf("Result = %+v, attendu vide", res)
			}
			if got := f.rec.keys(); len(got) != 1 {
				t.Errorf("appels = %v, aucun sondage attendu après un refus", got)
			}
		})
	}
}

func TestNetlifyPollingToleratesTransientErrors(t *testing.T) {
	fastPolling(t, time.Millisecond, 5*time.Second)
	f, n := newFakeNetlify(t, &fakeNetlify{respond: netlifySiteJSON, states: []string{"502", "429", "ready"}})
	if _, err := n.Deploy(context.Background(), "", siteFiles()); err != nil {
		t.Fatalf("Deploy : %v", err)
	}
	if got := f.polls.Load(); got != 3 {
		t.Errorf("%d sondages, attendu 3", got)
	}
}

// Plafond de suivi atteint sans verdict : succès documenté, l'URL est rendue.
func TestNetlifyPollTimeoutIsSuccess(t *testing.T) {
	fastPolling(t, time.Millisecond, 40*time.Millisecond)
	f, n := newFakeNetlify(t, &fakeNetlify{respond: netlifySiteJSON, states: []string{"processing"}})

	res, err := n.Deploy(context.Background(), "", siteFiles())
	if err != nil {
		t.Fatalf("Deploy : %v", err)
	}
	if res.URL != "https://brave-curie-123.netlify.app" || res.DeployID != "dep-1" {
		t.Errorf("Result = %+v", res)
	}
	if got := f.polls.Load(); got < 2 {
		t.Errorf("%d sondage(s), attendu plusieurs avant l'abandon", got)
	}
}

func TestNetlifyContextCancelStopsPolling(t *testing.T) {
	// Plafond très large : seul le contexte peut interrompre le suivi.
	fastPolling(t, time.Millisecond, 30*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f, n := newFakeNetlify(t, &fakeNetlify{
		respond: netlifySiteJSON,
		states:  []string{"processing"},
		onPoll: func(n int) {
			if n == 3 {
				cancel()
			}
		},
	})

	start := time.Now()
	res, err := n.Deploy(ctx, "", siteFiles())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("erreur = %v, attendu context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("retour après %v : l'annulation n'a pas interrompu le suivi", elapsed)
	}
	if res.Site != "site-123" {
		t.Errorf("Result.Site = %q, attendu le site créé", res.Site)
	}
	after := f.polls.Load()
	time.Sleep(30 * time.Millisecond)
	if got := f.polls.Load(); got != after || got > 4 {
		t.Errorf("sondages : %d puis %d — le suivi continue après l'annulation", after, got)
	}
}

func TestNetlifyNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // plus personne n'écoute : connexion refusée

	n := &Netlify{Token: netlifyToken, BaseURL: base}
	_, err := n.Deploy(context.Background(), "", siteFiles())
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("erreur = %v, attendu *Error", err)
	}
	if pe.Status != 0 || pe.Code != CodeUpstream {
		t.Errorf("Error = %+v, attendu statut 0 / upstream", pe)
	}
	if strings.Contains(pe.Message, base) {
		t.Errorf("l'URL de l'API apparaît dans le message : %q", pe.Message)
	}
}

func TestNetlifyRejectsBadInputBeforeAnyCall(t *testing.T) {
	cases := []struct {
		name  string
		token string
		site  string
		files []File
	}{
		{"fichiers invalides", netlifyToken, "", []File{{Path: "styles.css"}}},
		{"jeton vide", "   ", "", siteFiles()},
		{"jeton avec saut de ligne", "abc\ndef", "", siteFiles()},
		{"site avec slash", netlifyToken, "a/b", siteFiles()},
		{"site remontant", netlifyToken, "../accounts", siteFiles()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, n := newFakeNetlify(t, &fakeNetlify{respond: netlifySiteJSON, states: []string{"ready"}})
			n.Token = c.token
			_, err := n.Deploy(context.Background(), c.site, c.files)
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("erreur = %v, attendu ErrInvalidInput", err)
			}
			if got := f.rec.keys(); len(got) != 0 {
				t.Errorf("appels réseau malgré une entrée invalide : %v", got)
			}
		})
	}
}

func TestBuildZipDeterministic(t *testing.T) {
	files := siteFiles()
	reversed := []File{files[2], files[1], files[0]}
	a, err := buildZip(files)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildZip(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("le zip dépend de l'ordre d'arrivée des fichiers")
	}
	if files[0].Path != "index.html" {
		t.Error("buildZip a réordonné la tranche de l'appelant")
	}
}
