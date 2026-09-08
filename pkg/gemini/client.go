package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"nadaprod/pkg/prompts"
	"net/http"
	"strings"
	"time"
)

const geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

type Client struct {
	apiKey string
	model  string
	http   *http.Client
}

func NewClient(apiKey, model string) *Client {
	return &Client{
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
func (g *Client) GenerateBlock(ctx context.Context, spec prompts.BlockSpec, req GenerateRequest) (string, error) {
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

	const maxAttempts = 2 // 1 retry sur erreur transitoire
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
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
			if attempt+1 < maxAttempts { // pas d'attente après la dernière tentative
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(time.Duration(attempt+1) * time.Second):
				}
			}
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
