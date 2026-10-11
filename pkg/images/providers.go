package images

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	pexelsBaseURL   = "https://api.pexels.com"
	unsplashBaseURL = "https://api.unsplash.com"
	defaultTimeout  = 10 * time.Second
	maxBodyBytes    = 4 << 20 // garde-fou : 30 résultats pèsent quelques dizaines de Ko
	userAgent       = "nadaprod/1.0"
)

// apiError : réponse non-200 d'un fournisseur. Typée pour que Resolve puisse
// arrêter les frais quand la clé est refusée ou le quota épuisé.
type apiError struct {
	provider string
	status   int
	quota    bool
	detail   string
}

func (e *apiError) Error() string {
	switch {
	case e.quota:
		return fmt.Sprintf("%s : quota atteint (HTTP %d)", e.provider, e.status)
	case e.status == http.StatusUnauthorized || e.status == http.StatusForbidden:
		return fmt.Sprintf("%s : clé API refusée (HTTP %d)", e.provider, e.status)
	case e.detail == "":
		return fmt.Sprintf("%s : erreur HTTP %d", e.provider, e.status)
	}
	return fmt.Sprintf("%s : erreur HTTP %d : %s", e.provider, e.status, e.detail)
}

// fatal : réessayer tout de suite avec une autre requête échouerait pareil.
func (e *apiError) fatal() bool {
	return e.quota || e.status == http.StatusUnauthorized || e.status == http.StatusForbidden
}

func isFatal(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.fatal()
}

func newAPIError(provider string, resp *http.Response, body []byte) *apiError {
	e := &apiError{provider: provider, status: resp.StatusCode, detail: truncate(strings.TrimSpace(string(body)), 200)}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		e.quota = true
	case resp.StatusCode == http.StatusForbidden:
		// Unsplash signale un quota épuisé par un 403 « Rate Limit Exceeded »,
		// pas par un 429 : sans ce cas, on accuserait la clé à tort.
		e.quota = resp.Header.Get("X-Ratelimit-Remaining") == "0" ||
			strings.Contains(strings.ToLower(e.detail), "rate limit")
	}
	return e
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// getJSON : GET + décodage, commun aux deux fournisseurs. Les clés voyagent en
// en-tête, jamais dans l'URL : les erreurs réseau (qui citent l'URL) ne les
// divulguent donc pas.
func getJSON(ctx context.Context, client *http.Client, provider, endpoint string, header map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%s : requête invalide : %w", provider, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s injoignable : %w", provider, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("%s : lecture de la réponse : %w", provider, err)
	}
	if resp.StatusCode != http.StatusOK {
		return newAPIError(provider, resp, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s : réponse illisible : %w", provider, err)
	}
	return nil
}

// api : ce que partagent les deux fournisseurs — clé, URL de base, client.
type api struct {
	key  string
	base string
	http *http.Client
}

// newAPI : baseURL vide ⇒ fallback (API officielle) ; client nil ⇒ timeout 10 s.
func newAPI(key, baseURL, fallback string, client *http.Client) api {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return api{
		key:  strings.TrimSpace(key),
		base: cmp.Or(strings.TrimRight(strings.TrimSpace(baseURL), "/"), fallback),
		http: client,
	}
}

// searchParams valide les entrées communes. Les fournisseurs restent
// utilisables seuls, sans passer par Service : ils ne supposent donc pas que
// la requête a déjà été nettoyée.
func searchParams(provider, query, orientation string, perPage int) (url.Values, string, error) {
	q := sanitizeQuery(query)
	if q == "" {
		return nil, "", fmt.Errorf("%s : requête vide", provider)
	}
	v := url.Values{}
	v.Set("query", q)
	v.Set("per_page", strconv.Itoa(clampPerPage(perPage)))
	return v, normalizeOrientation(orientation), nil
}

// ---------- Pexels ----------

type pexels struct{ api }

// NewPexels : baseURL vide ⇒ API officielle ; client nil ⇒ timeout 10 s.
func NewPexels(apiKey, baseURL string, client *http.Client) Provider {
	return &pexels{newAPI(apiKey, baseURL, pexelsBaseURL, client)}
}

func (p *pexels) Name() string { return "pexels" }

type pexelsResponse struct {
	Photos []struct {
		ID              int64  `json:"id"`
		Width           int    `json:"width"`
		Height          int    `json:"height"`
		URL             string `json:"url"`
		Photographer    string `json:"photographer"`
		PhotographerURL string `json:"photographer_url"`
		Alt             string `json:"alt"`
		Src             struct {
			Large2x string `json:"large2x"`
			Large   string `json:"large"`
			Medium  string `json:"medium"`
		} `json:"src"`
	} `json:"photos"`
}

func (p *pexels) Search(ctx context.Context, query, orientation string, perPage int) ([]Photo, error) {
	v, o, err := searchParams(p.Name(), query, orientation, perPage)
	if err != nil {
		return nil, err
	}
	if o != "" {
		v.Set("orientation", o)
	}
	var out pexelsResponse
	// Pexels attend la clé nue, sans préfixe « Bearer ».
	err = getJSON(ctx, p.http, p.Name(), p.base+"/v1/search?"+v.Encode(), map[string]string{"Authorization": p.key}, &out)
	if err != nil {
		return nil, err
	}
	photos := make([]Photo, 0, len(out.Photos))
	for _, r := range out.Photos {
		src := cmp.Or(r.Src.Large2x, r.Src.Large)
		if src == "" {
			continue // sans URL exploitable, la photo ne sert à rien
		}
		photos = append(photos, Photo{
			ID:        strconv.FormatInt(r.ID, 10),
			Provider:  p.Name(),
			URL:       src,
			Thumb:     cmp.Or(r.Src.Medium, src),
			Width:     r.Width,
			Height:    r.Height,
			Alt:       strings.TrimSpace(r.Alt),
			Author:    strings.TrimSpace(r.Photographer),
			AuthorURL: r.PhotographerURL,
			PageURL:   r.URL,
		})
	}
	return photos, nil
}

// ---------- Unsplash ----------

type unsplash struct{ api }

// NewUnsplash : baseURL vide ⇒ API officielle ; client nil ⇒ timeout 10 s.
func NewUnsplash(accessKey, baseURL string, client *http.Client) Provider {
	return &unsplash{newAPI(accessKey, baseURL, unsplashBaseURL, client)}
}

func (u *unsplash) Name() string { return "unsplash" }

type unsplashResponse struct {
	Results []struct {
		ID             string `json:"id"`
		Width          int    `json:"width"`
		Height         int    `json:"height"`
		AltDescription string `json:"alt_description"`
		Description    string `json:"description"`
		URLs           struct {
			Raw     string `json:"raw"`
			Regular string `json:"regular"`
			Small   string `json:"small"`
		} `json:"urls"`
		Links struct {
			HTML string `json:"html"`
		} `json:"links"`
		User struct {
			Name  string `json:"name"`
			Links struct {
				HTML string `json:"html"`
			} `json:"links"`
		} `json:"user"`
	} `json:"results"`
}

func (u *unsplash) Search(ctx context.Context, query, orientation string, perPage int) ([]Photo, error) {
	v, o, err := searchParams(u.Name(), query, orientation, perPage)
	if err != nil {
		return nil, err
	}
	switch o {
	case "square":
		v.Set("orientation", "squarish") // vocabulaire propre à Unsplash
	case "landscape", "portrait":
		v.Set("orientation", o)
	}
	var out unsplashResponse
	err = getJSON(ctx, u.http, u.Name(), u.base+"/search/photos?"+v.Encode(), map[string]string{
		"Authorization":  "Client-ID " + u.key,
		"Accept-Version": "v1",
	}, &out)
	if err != nil {
		return nil, err
	}
	photos := make([]Photo, 0, len(out.Results))
	for _, r := range out.Results {
		src := unsplashURL(r.URLs.Raw, r.URLs.Regular)
		if src == "" {
			continue
		}
		photos = append(photos, Photo{
			ID:        r.ID,
			Provider:  u.Name(),
			URL:       src,
			Thumb:     cmp.Or(r.URLs.Small, src),
			Width:     r.Width,
			Height:    r.Height,
			Alt:       cmp.Or(strings.TrimSpace(r.AltDescription), strings.TrimSpace(r.Description)),
			Author:    strings.TrimSpace(r.User.Name),
			AuthorURL: withUTM(r.User.Links.HTML),
			PageURL:   withUTM(r.Links.HTML),
		})
	}
	return photos, nil
}

// unsplashURL : « regular » plafonne à 1080 px. L'URL « raw » (imgix) accepte
// nos propres paramètres et porte déjà sa signature en query string : on y
// greffe 1600 px. Sans « ? » dans raw, on ne devine pas le format : regular.
func unsplashURL(raw, regular string) string {
	const params = "w=1600&q=80&auto=format&fit=crop"
	switch {
	case strings.Contains(raw, "?"):
		return raw + "&" + params
	case regular != "":
		return regular
	case raw != "":
		return raw + "?" + params
	}
	return ""
}

// withUTM : les consignes de l'API Unsplash imposent ces paramètres sur tout
// lien d'attribution (photographe, page de la photo).
func withUTM(link string) string {
	if link == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(link, "?") {
		sep = "&"
	}
	return link + sep + "utm_source=nadaprod&utm_medium=referral"
}
