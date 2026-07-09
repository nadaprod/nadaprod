package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

type GeminiClient struct {
	apiKey string
	model  string
	http   *http.Client
}

func NewGeminiClient(apiKey, model string) *GeminiClient {
	return &GeminiClient{
		apiKey: apiKey,
		model:  model,
		http:   &http.Client{Timeout: 90 * time.Second},
	}
}

// ---- Schéma minimal de l'API generateContent ----

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
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// GenerateBlock appelle Gemini avec le prompt système ultra-spécialisé du bloc
// et renvoie un fragment HTML Tailwind nettoyé.
func (g *GeminiClient) GenerateBlock(ctx context.Context, spec BlockSpec, req GenerateRequest) (string, error) {
	userMsg := buildUserMessage(spec, req)

	body := gRequest{
		SystemInstruction: &gContent{Parts: []gPart{{Text: spec.SystemPrompt}}},
		Contents:          []gContent{{Role: "user", Parts: []gPart{{Text: userMsg}}}},
		GenerationConfig: gGenerationConfig{
			Temperature:     0.7,
			MaxOutputTokens: 8192,
		},
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf(geminiEndpoint, g.model)

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ { // 1 retry sur erreur transitoire
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-goog-api-key", g.apiKey)

		resp, err := g.http.Do(httpReq)
		if err != nil {
			lastErr = err
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("gemini %d : %s", resp.StatusCode, truncate(string(data), 300))
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		var out gResponse
		if err := json.Unmarshal(data, &out); err != nil {
			return "", fmt.Errorf("réponse Gemini illisible : %w", err)
		}
		if out.Error != nil {
			return "", fmt.Errorf("gemini %d : %s", out.Error.Code, out.Error.Message)
		}
		if len(out.Candidates) == 0 || len(out.Candidates[0].Content.Parts) == 0 {
			return "", fmt.Errorf("gemini : réponse vide (finishReason absent ou filtré)")
		}

		var sb strings.Builder
		for _, p := range out.Candidates[0].Content.Parts {
			sb.WriteString(p.Text)
		}
		return cleanHTML(sb.String()), nil
	}
	return "", fmt.Errorf("gemini injoignable après retry : %w", lastErr)
}

func buildUserMessage(spec BlockSpec, req GenerateRequest) string {
	var sb strings.Builder
	sb.WriteString("Brief du bloc « " + spec.Label + " » :\n")

	if strings.TrimSpace(req.Prompt) != "" {
		sb.WriteString(req.Prompt + "\n")
	} else {
		sb.WriteString("Aucune consigne particulière : propose une variante soignée et moderne.\n")
	}

	if req.Brand.Name != "" || req.Brand.Colors != "" || req.Brand.Tone != "" {
		sb.WriteString("\nContexte de marque :\n")
		if req.Brand.Name != "" {
			sb.WriteString("- Nom : " + req.Brand.Name + "\n")
		}
		if req.Brand.Colors != "" {
			sb.WriteString("- Palette : " + req.Brand.Colors + "\n")
		}
		if req.Brand.Tone != "" {
			sb.WriteString("- Ton : " + req.Brand.Tone + "\n")
		}
	}

	if strings.TrimSpace(req.CurrentHTML) != "" {
		sb.WriteString("\nVersion actuelle du bloc (à faire évoluer selon le brief, ne repars pas de zéro si le brief est une retouche) :\n")
		sb.WriteString("```html\n" + req.CurrentHTML + "\n```\n")
	}

	sb.WriteString("\nRéponds UNIQUEMENT avec le fragment HTML final.")
	return sb.String()
}

var fenceRe = regexp.MustCompile("(?s)```(?:html)?\\s*(.*?)```")

// cleanHTML retire les clôtures markdown et tout bavardage hors balises.
func cleanHTML(s string) string {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	// Coupe tout texte avant la première balise ouvrante.
	if i := strings.Index(s, "<"); i > 0 {
		s = s[i:]
	}
	// Coupe tout texte après la dernière balise fermante.
	if i := strings.LastIndex(s, ">"); i >= 0 && i < len(s)-1 {
		s = s[:i+1]
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
