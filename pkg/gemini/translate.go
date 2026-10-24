package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"nadaprod/pkg/prompts"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// TranslateRequest : un lot de segments à traduire (id → texte source, marqueurs
// <1>…</1> / <2/> inclus — voir pkg/i18n).
type TranslateRequest struct {
	From, To string
	Brand    Brand
	Items    map[string]string
}

// Translator : ce dont le studio a besoin pour traduire une page.
type Translator interface {
	Translate(ctx context.Context, req TranslateRequest) (map[string]string, error)
}

const translateChunk = 40 // segments par appel : une réponse JSON courte reste fiable

var jsonFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// Translate traduit les segments par lots de translateChunk (ordre des ids).
// Seuls les ids demandés, traduits par une chaîne non vide, sont renvoyés : un
// id absent est à retenter par l'appelant. En cas d'erreur, les lots déjà
// traduits sont renvoyés avec elle.
func (g *Client) Translate(ctx context.Context, req TranslateRequest) (map[string]string, error) {
	ids := slices.Sorted(maps.Keys(req.Items))
	out := make(map[string]string, len(ids))
	for chunk := range slices.Chunk(ids, translateChunk) {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		items := make(map[string]string, len(chunk))
		for _, id := range chunk {
			items[id] = req.Items[id]
		}
		raw, err := g.translateBody(req, items)
		if err != nil {
			return out, err
		}
		var text, blocked string
		if err := g.do(ctx, g.http, "generateContent", raw, func(r io.Reader) (err error) {
			text, blocked, err = readPlain(r)
			return err
		}); err != nil {
			return out, err
		}
		if blocked != "" {
			return out, &Error{Code: "blocked", Status: http.StatusOK, Message: "gemini a refusé cette traduction (" + blocked + ")"}
		}
		var got map[string]string
		if m := jsonFenceRe.FindStringSubmatch(text); m != nil {
			text = m[1]
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &got); err != nil {
			return out, &Error{Code: "upstream", Status: http.StatusOK, Message: "traduction Gemini illisible : " + err.Error()}
		}
		for id, v := range got {
			if _, asked := items[id]; asked && strings.TrimSpace(v) != "" {
				out[id] = v
			}
		}
	}
	return out, nil
}

func (g *Client) translateBody(req TranslateRequest, items map[string]string) ([]byte, error) {
	payload, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Langue source : %s (%s). Langue cible : %s (%s).\n", langName(req.From), req.From, langName(req.To), req.To)
	if req.Brand.Name != "" {
		sb.WriteString("Marque (ne pas traduire) : " + req.Brand.Name + "\n")
	}
	if req.Brand.Tone != "" {
		sb.WriteString("Ton : " + req.Brand.Tone + "\n")
	}
	sb.WriteString("\nSegments à traduire :\n")
	sb.Write(payload)
	return json.Marshal(gRequest{
		SystemInstruction: &gContent{Parts: []gPart{{Text: prompts.TranslatorPrompt}}},
		Contents:          []gContent{{Role: "user", Parts: []gPart{{Text: sb.String()}}}},
		GenerationConfig: gGenerationConfig{
			Temperature:      min(g.opts.Temperature, 0.3), // fidélité avant créativité
			MaxOutputTokens:  g.opts.MaxOutputTokens,
			ResponseMimeType: "application/json",
		},
	})
}
