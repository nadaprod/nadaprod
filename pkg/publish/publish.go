// Package publish : déploiement en un clic d'un petit site statique vers
// Netlify ou Cloudflare Pages, avec un jeton fourni par l'utilisateur final à
// chaque appel. Le jeton n'est jamais stocké, journalisé ni recopié dans un
// message d'erreur. Aucune dépendance à gin : le paquet ne connaît que
// net/http côté client.
package publish

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Plafonds d'un site publiable : on vise une landing page et ses assets, pas
// un hébergement généraliste (et tout transite en mémoire).
const (
	MaxFiles      = 200
	MaxTotalBytes = 25 << 20
)

// Valeurs possibles de Error.Code.
const (
	CodeAuth        = "auth"
	CodeNotFound    = "not_found"
	CodeRateLimited = "rate_limited"
	CodeInvalid     = "invalid"
	CodeUpstream    = "upstream"
)

// File : un fichier du site à déployer. Path est relatif, séparateur "/",
// sans "/" initial ("index.html", "assets/photo-1.jpg").
type File struct {
	Path string
	Data []byte
}

// Result : ce que l'UI affiche après un déploiement réussi.
type Result struct {
	Provider string `json:"provider"` // "netlify" | "cloudflare"
	URL      string `json:"url"`      // URL publique du site (https://…)
	DeployID string `json:"deploy_id"`
	Site     string `json:"site"` // identifiant/nom du site ou projet réellement utilisé (à réutiliser pour redéployer)
	AdminURL string `json:"admin_url,omitempty"`
}

// Error : erreur fournisseur exploitable par l'UI.
type Error struct {
	Provider string
	Status   int    // statut HTTP renvoyé par le fournisseur (0 si réseau)
	Code     string // "auth" (401/403), "not_found" (404), "rate_limited" (429), "invalid" (4xx autres), "upstream" (5xx/réseau)
	Message  string
}

func (e *Error) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s %d : %s", e.Provider, e.Status, e.Message)
	}
	return fmt.Sprintf("%s : %s", e.Provider, e.Message)
}

// ErrInvalidInput : entrée refusée avant tout appel réseau (fichiers, nom de
// site ou de projet, jeton absent). Se teste avec errors.Is pour répondre 400
// plutôt que 502 ; le message de l'erreur concrète reste affichable tel quel.
var ErrInvalidInput = errors.New("entrée invalide")

type inputError struct{ msg string }

func (e *inputError) Error() string        { return e.msg }
func (e *inputError) Is(target error) bool { return target == ErrInvalidInput }

func invalidf(format string, args ...any) error {
	return &inputError{msg: fmt.Sprintf(format, args...)}
}

var driveRe = regexp.MustCompile(`^[A-Za-z]:`)

// ValidateFiles : au moins un index.html à la racine, chemins relatifs propres
// (pas de "..", pas d'absolu, pas de backslash), ≤ 200 fichiers, ≤ 25 Mo au
// total. Appelée par les deux Deploy.
func ValidateFiles(files []File) error {
	if len(files) == 0 {
		return invalidf("aucun fichier à publier")
	}
	if len(files) > MaxFiles {
		return invalidf("trop de fichiers : %d (maximum %d)", len(files), MaxFiles)
	}
	seen := make(map[string]bool, len(files))
	total, hasIndex := 0, false
	for _, f := range files {
		if err := validatePath(f.Path); err != nil {
			return err
		}
		if seen[f.Path] {
			return invalidf("chemin en double : %q", f.Path)
		}
		seen[f.Path] = true
		if total += len(f.Data); total > MaxTotalBytes {
			return invalidf("site trop volumineux (maximum %d Mo)", MaxTotalBytes>>20)
		}
		hasIndex = hasIndex || f.Path == "index.html"
	}
	if !hasIndex {
		return invalidf("index.html manquant à la racine du site")
	}
	return nil
}

// validatePath : le chemin part tel quel dans une archive zip (Netlify) et
// dans un manifeste (Cloudflare) — on refuse donc tout ce qui n'est pas déjà
// sous forme canonique plutôt que de « réparer » en silence.
func validatePath(p string) error {
	switch {
	case p == "":
		return invalidf("chemin de fichier vide")
	case !utf8.ValidString(p):
		return invalidf("chemin invalide (UTF-8) : %q", p)
	case strings.Contains(p, `\`):
		return invalidf("chemin invalide (backslash) : %q", p)
	case strings.HasPrefix(p, "/"), driveRe.MatchString(p):
		return invalidf("chemin absolu refusé : %q", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return invalidf("chemin invalide (caractère de contrôle) : %q", p)
		}
	}
	if slices.Contains(strings.Split(p, "/"), "..") {
		return invalidf("chemin invalide (« .. ») : %q", p)
	}
	if path.Clean(p) != p {
		return invalidf("chemin non normalisé : %q", p)
	}
	return nil
}

// cleanToken : un jeton collé depuis un tableau de bord traîne souvent des
// espaces ; un caractère de contrôle ferait échouer net/http avec une erreur
// qui ressemblerait à une panne réseau.
func cleanToken(token, provider string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", invalidf("jeton %s manquant", provider)
	}
	for _, r := range token {
		if r <= 0x20 || r == 0x7f {
			return "", invalidf("jeton %s invalide", provider)
		}
	}
	return token, nil
}

func codeForStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return CodeAuth
	case status == http.StatusNotFound:
		return CodeNotFound
	case status == http.StatusTooManyRequests:
		return CodeRateLimited
	case status >= 400 && status < 500:
		return CodeInvalid
	}
	return CodeUpstream
}

const (
	userAgent = "nadaprod-publish/1.0"
	// Les réponses attendues pèsent quelques Ko ; la borne protège d'un
	// amont qui déverserait un corps arbitraire.
	maxResponseBytes = 8 << 20
)

var defaultHTTP = &http.Client{Timeout: 60 * time.Second}

// send : une requête authentifiée, corps de réponse lu en entier (borné).
// Contexte annulé ⇒ l'erreur du contexte (errors.Is fonctionne) ; toute autre
// panne de transport ⇒ *Error "upstream" de statut 0.
func send(ctx context.Context, hc *http.Client, provider, method, rawURL, bearer, contentType string, body []byte) (int, []byte, error) {
	if hc == nil {
		hc = defaultHTTP
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return 0, nil, fmt.Errorf("%s : requête invalide : %w", provider, err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, transportError(ctx, provider, 0, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, transportError(ctx, provider, resp.StatusCode, err)
	}
	return resp.StatusCode, data, nil
}

func transportError(ctx context.Context, provider string, status int, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s : %w", provider, ctx.Err())
	}
	msg := err.Error()
	// *url.Error préfixe l'URL complète (donc l'identifiant de compte
	// Cloudflare) : l'UI n'a besoin que de la cause.
	var ue *url.Error
	if errors.As(err, &ue) {
		msg = ue.Err.Error()
		if ue.Timeout() {
			msg = "délai d'attente dépassé"
		}
	}
	return upstreamError(provider, status, "réseau : "+msg)
}

// upstreamError : panne ou réponse aberrante côté fournisseur.
func upstreamError(provider string, status int, msg string) *Error {
	return &Error{Provider: provider, Status: status, Code: CodeUpstream, Message: msg}
}

// endpoint : URL de base (surchargeable en test) + chemin d'API.
func endpoint(base, fallback, p string) string {
	return strings.TrimRight(cmp.Or(base, fallback), "/") + p
}

// byPath : ordre canonique des fichiers (chemins uniques, cf. ValidateFiles).
func byPath(a, b File) int { return strings.Compare(a.Path, b.Path) }

// snippet : corps d'erreur non JSON ramené à une ligne affichable. Une page
// HTML (502 d'un proxy, etc.) n'apporte rien à l'UI : on la remplace par le
// libellé du statut.
func snippet(status int, body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" || strings.HasPrefix(s, "<") {
		if t := http.StatusText(status); t != "" {
			return t
		}
		return "réponse inattendue"
	}
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}
