package gemini

import (
	"fmt"
	"nadaprod/pkg/prompts"
	"regexp"
	"slices"
	"strings"
)

type Brand struct {
	Name   string `json:"name"`
	Colors string `json:"colors"` // ex: "fond sombre #0B0D12, accent violet #8B5CF6"
	Tone   string `json:"tone"`   // ex: "startup SaaS, moderne, direct"
}

// Theme : le mini design-system du projet. Quand il est actif, la page définit
// dans sa config Tailwind les échelles primary/accent, les polices display/sans
// et le rayon « brand » : les blocs doivent passer par ces jetons, ce qui rend
// un changement de thème instantané (aucune regénération).
type Theme struct {
	Enabled     bool   `json:"enabled"`
	Mode        string `json:"mode"`    // "light" | "dark" | "" (au choix du brief)
	Primary     string `json:"primary"` // hex, informatif pour le modèle
	Accent      string `json:"accent"`
	FontDisplay string `json:"font_display"`
	FontSans    string `json:"font_sans"`
	Radius      string `json:"radius"` // none | sm | md | lg | xl | full
	Style       string `json:"style"`  // direction libre : « minimal », « éditorial »…
}

// Sibling : un autre bloc de la page, transmis pour la cohérence d'ensemble.
type Sibling struct {
	Type string `json:"type"`
	HTML string `json:"html"`
}

const ModeRetheme = "retheme"

type GenerateRequest struct {
	Block       string    `json:"block" binding:"required"` // header, hero, features...
	Prompt      string    `json:"prompt"`
	Brand       Brand     `json:"brand"`
	CurrentHTML string    `json:"current_html"` // pour itérer sur un bloc existant
	Theme       *Theme    `json:"theme"`
	Siblings    []Sibling `json:"siblings"` // les autres blocs déjà générés
	Mode        string    `json:"mode"`     // "" | "retheme"
	Variant     int       `json:"variant"`  // 0 = aucune, 1..3 = direction artistique imposée
	Images      bool      `json:"images"`   // remplacer les placeholders par de vraies photos
}

type GenerateResponse struct {
	HTML      string `json:"html"`
	Block     string `json:"block"`
	Model     string `json:"model"`
	ElapsedMs int64  `json:"elapsed_ms"`
	Photos    int    `json:"photos"` // placeholders remplacés par de vraies photos
}

// Trois directions volontairement éloignées : des variantes qui se
// ressemblent ne servent à rien.
var variantBriefs = [...]string{
	"Direction A — sobre et éditoriale : beaucoup d'espace, typographie dominante, couleur de marque en touches rares, aucun effet décoratif.",
	"Direction B — audacieuse et contrastée : aplats francs de couleur de marque, formes fortes, hiérarchie très marquée, composition asymétrique si le bloc s'y prête.",
	"Direction C — riche et détaillée : cartes, dégradés subtils, micro-détails soignés (badges, icônes, séparateurs, halos), sensation premium.",
}

var radiusLabels = map[string]string{
	"none": "angles vifs", "sm": "très léger arrondi", "md": "arrondi modéré",
	"lg": "arrondi généreux", "xl": "très arrondi", "full": "formes pilule",
}

func themeSection(t *Theme) string {
	var sb strings.Builder
	sb.WriteString("\nDesign system de la page (OBLIGATOIRE — la config Tailwind de la page définit ces jetons) :\n")
	sb.WriteString("- Couleur de marque : classes primary-50 à primary-950 (bg-primary-600, text-primary-700, ring-primary-500, from-primary-500…). Couleur secondaire : accent-50 à accent-950. N'utilise AUCUNE autre teinte saturée (pas de violet-600, blue-500… codés en dur) ; les neutres (slate/zinc/white/black) restent libres.\n")
	if t.Primary != "" || t.Accent != "" {
		fmt.Fprintf(&sb, "  Pour information : primary ≈ %s, accent ≈ %s.\n", orDash(t.Primary), orDash(t.Accent))
	}
	sb.WriteString("- Typographie : font-display sur tous les titres, font-sans sur le texte courant")
	if t.FontDisplay != "" || t.FontSans != "" {
		fmt.Fprintf(&sb, " (display = %s, sans = %s)", orDash(t.FontDisplay), orDash(t.FontSans))
	}
	sb.WriteString(".\n")
	sb.WriteString("- Rayons : rounded-brand sur les boutons, champs et cartes")
	if l := radiusLabels[t.Radius]; l != "" {
		sb.WriteString(" (" + l + ")")
	}
	sb.WriteString(" ; rounded-full reste permis pour les avatars et pastilles.\n")
	switch t.Mode {
	case "dark":
		sb.WriteString("- Ambiance : page SOMBRE (fonds slate-950/900, textes clairs), sauf demande contraire du brief.\n")
	case "light":
		sb.WriteString("- Ambiance : page CLAIRE (fonds blancs/slate-50, textes sombres), sauf demande contraire du brief.\n")
	}
	if s := strings.TrimSpace(t.Style); s != "" {
		sb.WriteString("- Direction artistique : " + truncate(s, 200) + "\n")
	}
	return sb.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func buildUserMessage(spec prompts.BlockSpec, req GenerateRequest) string {
	var sb strings.Builder
	hasCurrent := strings.TrimSpace(req.CurrentHTML) != ""
	themed := req.Theme != nil && req.Theme.Enabled
	retheme := req.Mode == ModeRetheme && hasCurrent

	if retheme {
		sb.WriteString("RE-THÈME du bloc « " + spec.Label + " » : réécris la version actuelle pour qu'elle respecte STRICTEMENT le design system ci-dessous.\n")
		sb.WriteString("- Conserve à l'identique les textes, la structure, l'ordre des éléments, les images et les liens.\n")
		sb.WriteString("- Ne change que ce qui relève du style : couleurs (→ jetons primary/accent + neutres), polices (→ font-display / font-sans), rayons (→ rounded-brand), ambiance claire/sombre.\n")
	} else {
		sb.WriteString("Brief du bloc « " + spec.Label + " » :\n")
		if strings.TrimSpace(req.Prompt) != "" {
			sb.WriteString(req.Prompt + "\n")
		} else {
			sb.WriteString("Aucune consigne particulière : propose une variante soignée et moderne.\n")
		}
	}

	if req.Brand.Name != "" || req.Brand.Colors != "" || req.Brand.Tone != "" {
		sb.WriteString("\nContexte de marque :\n")
		if req.Brand.Name != "" {
			sb.WriteString("- Nom : " + req.Brand.Name + "\n")
		}
		// Avec un design system actif, la palette libre ne doit pas le contredire.
		if req.Brand.Colors != "" && !themed {
			sb.WriteString("- Palette : " + req.Brand.Colors + "\n")
		}
		if req.Brand.Tone != "" {
			sb.WriteString("- Ton : " + req.Brand.Tone + "\n")
		}
	}

	if themed {
		sb.WriteString(themeSection(req.Theme))
	}

	if !retheme {
		if sum := SummarizeSiblings(req.Siblings); sum != "" {
			sb.WriteString("\nCohérence avec le reste de la page (blocs déjà en place — aligne-toi sur eux, ne les répète pas) :\n")
			sb.WriteString(sum)
		}
		if req.Variant >= 1 && req.Variant <= len(variantBriefs) {
			sb.WriteString("\nDirection artistique imposée pour cette proposition :\n" + variantBriefs[req.Variant-1] + "\n")
		}
	}

	if hasCurrent {
		if retheme {
			sb.WriteString("\nVersion actuelle du bloc :\n")
		} else if req.Variant > 0 {
			sb.WriteString("\nVersion actuelle du bloc (reprends-en le CONTENU, mais propose une mise en forme franchement différente) :\n")
		} else {
			sb.WriteString("\nVersion actuelle du bloc (à faire évoluer selon le brief, ne repars pas de zéro si le brief est une retouche) :\n")
		}
		sb.WriteString("```html\n" + req.CurrentHTML + "\n```\n")
	}

	sb.WriteString("\nRéponds UNIQUEMENT avec le fragment HTML final.")
	return sb.String()
}

// ---------- Cohérence inter-blocs ----------

var (
	colorClassRe = regexp.MustCompile(`\b(?:bg|text|from|to|via|border|ring)-((?:[a-z]+-\d{2,3})|(?:\[#[0-9a-fA-F]{3,8}\]))`)
	headingRe    = regexp.MustCompile(`(?is)<h([1-3])\b[^>]*>(.*?)</h[1-3]>`)
	// Un CTA « plein » : lien ou bouton portant une classe de fond.
	ctaRe      = regexp.MustCompile(`(?is)<(?:a|button)\b[^>]*\bclass\s*=\s*"[^"]*\bbg-[^"]*"[^>]*>(.*?)</(?:a|button)>`)
	tagRe      = regexp.MustCompile(`(?s)<[^>]*>`)
	spacesRe   = regexp.MustCompile(`\s+`)
	darkBgRe   = regexp.MustCompile(`\bbg-(?:black|(?:slate|gray|zinc|neutral|stone)-(?:800|900|950)|primary-9(?:00|50))\b`)
	neutralsRe = regexp.MustCompile(`^(?:slate|gray|zinc|neutral|stone)-`)
)

func plainText(fragment string) string {
	return strings.TrimSpace(spacesRe.ReplaceAllString(tagRe.ReplaceAllString(fragment, " "), " "))
}

// SummarizeSiblings condense les blocs déjà générés en quelques lignes que le
// modèle peut suivre : titres posés, libellés des CTA, teintes dominantes,
// ambiance claire/sombre. Borné : le résumé ne doit pas noyer le brief.
func SummarizeSiblings(sibs []Sibling) string {
	const maxBlocks, maxLen = 12, 1500
	var sb strings.Builder
	colors := map[string]int{}
	var order []string
	n := 0
	for _, s := range sibs {
		if strings.TrimSpace(s.HTML) == "" {
			continue
		}
		if n++; n > maxBlocks {
			break
		}
		for _, m := range colorClassRe.FindAllStringSubmatch(s.HTML, -1) {
			if neutralsRe.MatchString(m[1]) {
				continue
			}
			if colors[m[1]] == 0 {
				order = append(order, m[1])
			}
			colors[m[1]]++
		}
		line := "- " + s.Type
		if darkBgRe.MatchString(s.HTML) {
			line += " (fond sombre)"
		}
		var heads []string
		for _, m := range headingRe.FindAllStringSubmatch(s.HTML, 2) {
			if t := plainText(m[2]); t != "" {
				heads = append(heads, "« "+truncate(t, 90)+" »")
			}
		}
		if len(heads) > 0 {
			line += " — titres : " + strings.Join(heads, ", ")
		}
		var ctas []string
		seen := map[string]bool{}
		for _, m := range ctaRe.FindAllStringSubmatch(s.HTML, 4) {
			if t := plainText(m[1]); t != "" && len([]rune(t)) <= 40 && !seen[t] {
				seen[t] = true
				ctas = append(ctas, "« "+t+" »")
			}
		}
		if len(ctas) > 0 {
			line += " — CTA : " + strings.Join(ctas, ", ")
		}
		sb.WriteString(line + "\n")
	}
	if n == 0 {
		return ""
	}
	// Teintes dominantes : les 5 plus fréquentes, à fréquence égale dans
	// l'ordre d'apparition (tri stable : sortie déterministe).
	slices.SortStableFunc(order, func(a, b string) int { return colors[b] - colors[a] })
	if len(order) > 0 {
		sb.WriteString("- Teintes dominantes déjà utilisées : " + strings.Join(order[:min(5, len(order))], ", ") + "\n")
	}
	sb.WriteString("- Réutilise les mêmes libellés de CTA et le même nom de produit ; un seul <h1> existe sur la page (dans le hero).\n")
	return truncate(sb.String(), maxLen)
}

// ---------- Filet de sécurité sur la sortie du modèle ----------

var (
	fenceRe  = regexp.MustCompile("(?s)```(?:html)?\\s*(.*?)```")
	scriptRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>|<script\b[^>]*/>`)
	// Flux en cours : une clôture ouvrante peut exister sans sa fermeture.
	openFenceRe = regexp.MustCompile("^```(?:html)?\\s*")
)

// cleanHTML retire les clôtures markdown et tout bavardage hors balises.
func cleanHTML(s string) string {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	} else {
		s = openFenceRe.ReplaceAllString(s, "") // réponse tronquée avant la clôture fermante
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

// Sanitize fait respecter la partie vérifiable du contrat : un bloc dont la
// spécialisation interdit JavaScript ne ressort jamais avec un <script>.
func Sanitize(html string, allowJS bool) string {
	if allowJS {
		return html
	}
	return strings.TrimSpace(scriptRe.ReplaceAllString(html, ""))
}

func truncate(s string, n int) string {
	r := []rune(s) // tronquer en runes : ne jamais couper un caractère UTF-8
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
