package publish

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorded : une requête reçue par un faux fournisseur. Les corps sont
// conservés pour que les assertions se fassent dans la goroutine du test.
type recorded struct {
	Method      string
	Path        string
	Auth        string
	ContentType string
	Body        []byte
}

func (r recorded) key() string { return r.Method + " " + r.Path }

type recorder struct {
	mu   sync.Mutex
	reqs []recorded
}

func (rc *recorder) add(r *http.Request) recorded {
	body, _ := io.ReadAll(r.Body)
	rec := recorded{
		Method:      r.Method,
		Path:        r.URL.Path,
		Auth:        r.Header.Get("Authorization"),
		ContentType: r.Header.Get("Content-Type"),
		Body:        body,
	}
	rc.mu.Lock()
	rc.reqs = append(rc.reqs, rec)
	rc.mu.Unlock()
	return rec
}

func (rc *recorder) all() []recorded {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]recorded(nil), rc.reqs...)
}

func (rc *recorder) keys() []string {
	var out []string
	for _, r := range rc.all() {
		out = append(out, r.key())
	}
	return out
}

func (rc *recorder) count(key string) int {
	n := 0
	for _, r := range rc.all() {
		if r.key() == key {
			n++
		}
	}
	return n
}

// siteFiles : le site type, volontairement hors ordre alphabétique pour
// vérifier que zip, empreintes et lots sont triés.
func siteFiles() []File {
	return []File{
		{Path: "index.html", Data: []byte("<!doctype html><h1>Nébula Café</h1>")},
		{Path: "styles.css", Data: []byte("body{margin:0}")},
		{Path: "assets/photo-1.jpg", Data: []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10}},
	}
}

// fastPolling : ramène le suivi Netlify à l'échelle d'un test.
func fastPolling(t *testing.T, interval, timeout time.Duration) {
	t.Helper()
	oldInterval, oldTimeout := netlifyPollInterval, netlifyPollTimeout
	netlifyPollInterval, netlifyPollTimeout = interval, timeout
	t.Cleanup(func() { netlifyPollInterval, netlifyPollTimeout = oldInterval, oldTimeout })
}

func TestValidateFiles(t *testing.T) {
	index := File{Path: "index.html", Data: []byte("<h1>ok</h1>")}
	big := make([]byte, MaxTotalBytes+1)

	many := func(n int) []File {
		files := []File{index}
		for i := 1; i < n; i++ {
			files = append(files, File{Path: fmt.Sprintf("p/%03d.html", i)})
		}
		return files
	}

	cases := []struct {
		name  string
		files []File
		want  string // fragment attendu du message ; "" ⇒ valide
	}{
		{"site minimal", []File{index}, ""},
		{"site type", siteFiles(), ""},
		{"200 fichiers", many(MaxFiles), ""},
		{"25 Mo pile", []File{index, {Path: "big.bin", Data: big[:MaxTotalBytes-len(index.Data)]}}, ""},

		{"aucun fichier", nil, "aucun fichier"},
		{"index manquant", []File{{Path: "styles.css"}}, "index.html manquant"},
		{"index seulement en sous-dossier", []File{{Path: "sub/index.html"}}, "index.html manquant"},
		{"remontée ../", []File{index, {Path: "../secret.txt"}}, ".."},
		{"remontée au milieu", []File{index, {Path: "assets/../../x.css"}}, ".."},
		{"chemin absolu", []File{index, {Path: "/etc/passwd"}}, "absolu"},
		{"lecteur Windows", []File{index, {Path: "C:/site/a.css"}}, "absolu"},
		{"backslash", []File{index, {Path: `assets\photo.jpg`}}, "backslash"},
		{"chemin vide", []File{index, {Path: ""}}, "vide"},
		{"préfixe ./", []File{index, {Path: "./a.css"}}, "non normalisé"},
		{"double slash", []File{index, {Path: "assets//a.css"}}, "non normalisé"},
		{"slash final", []File{index, {Path: "assets/"}}, "non normalisé"},
		{"caractère de contrôle", []File{index, {Path: "a\x00b.css"}}, "contrôle"},
		{"doublon", []File{index, {Path: "a.css"}, {Path: "a.css"}}, "double"},
		{"trop de fichiers", many(MaxFiles + 1), "trop de fichiers"},
		{"trop volumineux", []File{index, {Path: "big.bin", Data: big}}, "volumineux"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateFiles(c.files)
			if c.want == "" {
				if err != nil {
					t.Fatalf("erreur inattendue : %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("aucune erreur, attendu un message contenant %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("message %q, attendu un fragment %q", err, c.want)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("errors.Is(err, ErrInvalidInput) = false pour %v", err)
			}
		})
	}
}

func TestErrorString(t *testing.T) {
	cases := []struct {
		err  *Error
		want string
	}{
		{&Error{Provider: "netlify", Status: 401, Code: CodeAuth, Message: "Access Denied"}, "netlify 401 : Access Denied"},
		{&Error{Provider: "cloudflare", Code: CodeUpstream, Message: "réseau : connexion refusée"}, "cloudflare : réseau : connexion refusée"},
	}
	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Errorf("Error() = %q, attendu %q", got, c.want)
		}
	}
}

func TestCodeForStatus(t *testing.T) {
	cases := map[int]string{
		401: CodeAuth, 403: CodeAuth, 404: CodeNotFound, 429: CodeRateLimited,
		400: CodeInvalid, 409: CodeInvalid, 422: CodeInvalid,
		500: CodeUpstream, 502: CodeUpstream, 503: CodeUpstream,
		200: CodeUpstream, // succès HTTP mais réponse refusée (success:false, état « error »)
	}
	for status, want := range cases {
		if got := codeForStatus(status); got != want {
			t.Errorf("codeForStatus(%d) = %q, attendu %q", status, got, want)
		}
	}
}
