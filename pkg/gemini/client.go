package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"nadaprod/pkg/prompts"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultEndpoint = "https://generativelanguage.googleapis.com/v1beta/models"
	maxAttempts     = 2 // 1 retry sur erreur transitoire
)

// Generator : ce dont le studio a besoin pour générer un bloc. Interface pour
// pouvoir brancher un faux générateur dans les tests des routes — et, demain,
// un orchestrateur de page entière qui enchaîne les blocs côté serveur.
type Generator interface {
	Model() string
	GenerateBlock(ctx context.Context, spec prompts.BlockSpec, req GenerateRequest) (string, error)
	// StreamBlock appelle onDelta à chaque fragment de texte reçu, puis renvoie
	// le HTML final nettoyé (le flux brut peut contenir des clôtures markdown).
	StreamBlock(ctx context.Context, spec prompts.BlockSpec, req GenerateRequest, onDelta func(string)) (string, error)
}

// Options : paramètres de génération (GEMINI_TEMPERATURE, GEMINI_MAX_TOKENS).
type Options struct {
	Temperature     float64
	MaxOutputTokens int
	Endpoint        string // surchargé en test
}

type Client struct {
	apiKey string
	model  string
	opts   Options
	http   *http.Client // appels simples (timeout global)
	stream *http.Client // flux : pas de timeout global, c'est le contexte qui borne
}

func NewClient(apiKey, model string, opts Options) *Client {
	if opts.Temperature <= 0 || opts.Temperature > 2 {
		opts.Temperature = 0.7
	}
	if opts.MaxOutputTokens <= 0 {
		opts.MaxOutputTokens = 8192
	}
	if opts.Endpoint == "" {
		opts.Endpoint = defaultEndpoint
	}
	return &Client{
		apiKey: apiKey,
		model:  model,
		opts:   opts,
		http:   &http.Client{Timeout: 90 * time.Second},
		stream: &http.Client{},
	}
}

func (g *Client) Model() string { return g.model }

// ---------- Erreurs typées ----------

// Error : échec de génération exploitable par l'UI (champ "code" de la
// réponse) — le studio affiche une aide adaptée plutôt qu'un message brut.
type Error struct {
	Code       string // quota | auth | blocked | invalid | upstream | network | empty
	Status     int    // statut HTTP renvoyé par Gemini (0 si réseau)
	Message    string
	RetryAfter int // secondes conseillées avant de réessayer (quota), 0 si inconnu
}

func (e *Error) Error() string { return e.Message }

// HTTPStatus : statut à renvoyer au navigateur pour cette erreur.
func (e *Error) HTTPStatus() int {
	switch e.Code {
	case "quota":
		return http.StatusTooManyRequests
	case "blocked", "empty":
		return http.StatusUnprocessableEntity
	case "invalid":
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

func (e *Error) transient() bool {
	return e.Code == "quota" || e.Code == "upstream" || e.Code == "network"
}

type gErrorBody struct {
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type       string `json:"@type"`
			RetryDelay string `json:"retryDelay"`
			Reason     string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

var retryDelayRe = regexp.MustCompile(`^(\d+)(?:\.\d+)?s$`)

// classify transforme une réponse d'erreur de Gemini en *Error.
func classify(status int, body []byte) *Error {
	e := &Error{Status: status, Code: "upstream", Message: fmt.Sprintf("gemini %d : %s", status, truncate(string(body), 300))}
	var parsed gErrorBody
	apiStatus, reason := "", ""
	if json.Unmarshal(body, &parsed) == nil && parsed.Error != nil {
		apiStatus = parsed.Error.Status
		e.Message = fmt.Sprintf("gemini %d : %s", status, truncate(parsed.Error.Message, 300))
		for _, d := range parsed.Error.Details {
			if m := retryDelayRe.FindStringSubmatch(d.RetryDelay); m != nil {
				e.RetryAfter, _ = strconv.Atoi(m[1])
			}
			if d.Reason != "" {
				reason = d.Reason
			}
		}
	}
	switch {
	case status == http.StatusTooManyRequests || apiStatus == "RESOURCE_EXHAUSTED":
		e.Code = "quota"
	case status == http.StatusUnauthorized || status == http.StatusForbidden ||
		reason == "API_KEY_INVALID" || apiStatus == "PERMISSION_DENIED" || apiStatus == "UNAUTHENTICATED":
		e.Code = "auth"
	case status >= 500:
		e.Code = "upstream"
	case status >= 400:
		e.Code = "invalid" // modèle inconnu, requête refusée…
	}
	return e
}

// ---------- Schéma minimal de l'API generateContent ----------

type gPart struct {
	Text string `json:"text"`
}
type gContent struct {
	Role  string  `json:"role,omitempty"`
	Parts []gPart `json:"parts"`
}
type gGenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}
type gRequest struct {
	SystemInstruction *gContent         `json:"systemInstruction,omitempty"`
	Contents          []gContent        `json:"contents"`
	GenerationConfig  gGenerationConfig `json:"generationConfig"`
}
type gResponse struct {
	Candidates []struct {
		Content      gContent `json:"content"`
		FinishReason string   `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

// text concatène les parts du premier candidat ; blocked signale un refus.
func (r gResponse) text() (s string, blocked string) {
	if r.PromptFeedback != nil && r.PromptFeedback.BlockReason != "" {
		return "", r.PromptFeedback.BlockReason
	}
	if len(r.Candidates) == 0 {
		return "", ""
	}
	var sb strings.Builder
	for _, p := range r.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	switch fr := r.Candidates[0].FinishReason; fr {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "RECITATION", "SPII":
		if sb.Len() == 0 {
			return "", fr
		}
	}
	return sb.String(), ""
}

func (g *Client) body(spec prompts.BlockSpec, req GenerateRequest) ([]byte, error) {
	temp := g.opts.Temperature
	if req.Variant > 0 {
		temp = min(temp+0.25, 1.2) // des variantes doivent diverger
	}
	if req.Mode == ModeRetheme {
		temp = min(temp, 0.4) // un re-thème doit rester fidèle
	}
	return json.Marshal(gRequest{
		SystemInstruction: &gContent{Parts: []gPart{{Text: spec.SystemPrompt}}},
		Contents:          []gContent{{Role: "user", Parts: []gPart{{Text: buildUserMessage(spec, req)}}}},
		GenerationConfig:  gGenerationConfig{Temperature: temp, MaxOutputTokens: g.opts.MaxOutputTokens},
	})
}

func (g *Client) post(ctx context.Context, hc *http.Client, url string, raw []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", g.apiKey)
	return hc.Do(httpReq)
}

// wait : pause avant retry, interrompue si le client abandonne.
func wait(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func netError(err error) *Error {
	return &Error{Code: "network", Message: "gemini injoignable : " + err.Error()}
}

// finish applique le filet de sécurité commun aux deux modes.
func finish(spec prompts.BlockSpec, text, blocked string) (string, error) {
	if blocked != "" {
		return "", &Error{Code: "blocked", Status: http.StatusOK, Message: "gemini a refusé ce brief (" + blocked + ")"}
	}
	html := Sanitize(cleanHTML(text), spec.AllowJS)
	if html == "" {
		return "", &Error{Code: "empty", Status: http.StatusOK, Message: "gemini : réponse vide (filtrée ou tronquée)"}
	}
	return html, nil
}

// GenerateBlock appelle Gemini avec le prompt système ultra-spécialisé du bloc
// et renvoie un fragment HTML Tailwind nettoyé.
func (g *Client) GenerateBlock(ctx context.Context, spec prompts.BlockSpec, req GenerateRequest) (string, error) {
	return g.generate(ctx, spec, req, g.http, "generateContent", func(r io.Reader) (string, string, error) {
		data, err := io.ReadAll(r)
		if err != nil {
			return "", "", err
		}
		var out gResponse
		if err := json.Unmarshal(data, &out); err != nil {
			return "", "", &Error{Code: "upstream", Status: http.StatusOK, Message: "réponse Gemini illisible : " + err.Error()}
		}
		text, blocked := out.text()
		return text, blocked, nil
	})
}

// StreamBlock : même contrat, via streamGenerateContent (SSE). Le retry n'est
// tenté que si rien n'a encore été transmis — on ne rejoue pas un flux entamé.
func (g *Client) StreamBlock(ctx context.Context, spec prompts.BlockSpec, req GenerateRequest, onDelta func(string)) (string, error) {
	return g.generate(ctx, spec, req, g.stream, "streamGenerateContent?alt=sse", func(r io.Reader) (string, string, error) {
		text, blocked, sent, err := readSSE(r, onDelta)
		if err != nil && sent {
			return "", "", netError(err) // flux entamé : pas de retry
		}
		return text, blocked, err
	})
}

// generate : POST vers method, un retry sur erreur transitoire, puis le filet
// de sécurité commun. read consomme une réponse 200 : une *Error qu'il renvoie
// est définitive, toute autre erreur est réseau (donc retentée).
func (g *Client) generate(ctx context.Context, spec prompts.BlockSpec, req GenerateRequest, hc *http.Client, method string,
	read func(io.Reader) (text, blocked string, err error)) (string, error) {
	raw, err := g.body(spec, req)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/%s:%s", g.opts.Endpoint, g.model, method)

	var lastErr *Error
	for attempt := range maxAttempts {
		if attempt > 0 {
			if err := wait(ctx, time.Duration(attempt)*time.Second); err != nil {
				return "", err
			}
		}
		resp, err := g.post(ctx, hc, url, raw)
		if err == nil {
			if resp.StatusCode != http.StatusOK {
				data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				resp.Body.Close()
				if lastErr = classify(resp.StatusCode, data); lastErr.transient() {
					continue
				}
				return "", lastErr
			}
			var text, blocked string
			text, blocked, err = read(resp.Body)
			resp.Body.Close()
			if err == nil {
				return finish(spec, text, blocked)
			}
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if ge, ok := errors.AsType[*Error](err); ok {
			return "", ge
		}
		lastErr = netError(err)
	}
	return "", lastErr
}

// readSSE lit les événements « data: {json} » du flux Gemini.
func readSSE(r io.Reader, onDelta func(string)) (text, blocked string, sent bool, err error) {
	var sb strings.Builder
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20) // un événement peut dépasser 64 Ko
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var chunk gResponse
		if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &chunk) != nil {
			continue
		}
		t, b := chunk.text()
		if b != "" {
			blocked = b
		}
		if t != "" {
			sb.WriteString(t)
			sent = true
			if onDelta != nil {
				onDelta(t)
			}
		}
	}
	return sb.String(), blocked, sent, sc.Err() // nil en fin de flux normale
}
