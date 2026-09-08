package gemini

import (
	"nadaprod/pkg/prompts"
	"regexp"
	"strings"
)

type Brand struct {
	Name   string `json:"name"`
	Colors string `json:"colors"` // ex: "fond sombre #0B0D12, accent violet #8B5CF6"
	Tone   string `json:"tone"`   // ex: "startup SaaS, moderne, direct"
}

type GenerateRequest struct {
	Block       string `json:"block" binding:"required"` // header, hero, features...
	Prompt      string `json:"prompt"`
	Brand       Brand  `json:"brand"`
	CurrentHTML string `json:"current_html"` // pour itérer sur un bloc existant
}

type GenerateResponse struct {
	HTML      string `json:"html"`
	Block     string `json:"block"`
	Model     string `json:"model"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

func buildUserMessage(spec prompts.BlockSpec, req GenerateRequest) string {
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
	r := []rune(s) // tronquer en runes : ne jamais couper un caractère UTF-8
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
