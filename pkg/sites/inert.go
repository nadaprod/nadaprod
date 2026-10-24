package sites

import (
	"bytes"
	"regexp"
	"strings"
)

// Mode visuel : la page s'affiche avec ses scripts rendus inertes. Sinon
// l'« Enregistrer » sérialiserait le DOM *après* leur exécution — langue du
// visiteur appliquée, classes d'animation, contenu généré… tout finirait
// figé dans la source. inject.js rend à chaque script son type d'origine au
// moment de la sérialisation.
//
// Seul Tailwind reste actif (CDN + config inline) : sans lui, la page
// éditée n'aurait plus de styles.

// inertType marque un script neutralisé ; data-np-type garde son type d'origine.
const inertType = "text/x-np-inert"

var (
	scriptOpenRe  = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	scriptCloseRe = regexp.MustCompile(`(?i)</script\s*>`)
	// (^|\s) et non \b : « data-type= » ne doit pas passer pour « type= ».
	typeAttrRe = regexp.MustCompile(`(?i)(^|\s)type\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
)

// jsType : ce type de script s'exécute-t-il ? (vide, JS classique, module)
func jsType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", "text/javascript", "application/javascript", "module",
		"text/ecmascript", "application/ecmascript":
		return true
	}
	return false
}

func keepScript(attrs, body string) bool {
	return strings.Contains(attrs, "cdn.tailwindcss.com") || strings.Contains(body, "tailwind.config")
}

func inertScripts(raw []byte) []byte {
	var out bytes.Buffer
	rest := raw
	for {
		loc := scriptOpenRe.FindIndex(rest)
		if loc == nil {
			out.Write(rest)
			return out.Bytes()
		}
		tag := string(rest[loc[0]:loc[1]])
		after := rest[loc[1]:]
		end := scriptCloseRe.FindIndex(after)
		body := ""
		if end != nil {
			body = string(after[:end[0]])
		}
		out.Write(rest[:loc[0]])
		attrs := tag[len("<script") : len(tag)-1]
		orig, hasType := "", false
		if m := typeAttrRe.FindStringSubmatchIndex(attrs); m != nil {
			hasType = true
			orig = strings.Trim(attrs[m[4]:m[5]], `"'`)
			attrs = attrs[:m[2]] + attrs[m[1]:]
		}
		if !jsType(orig) || keepScript(tag, body) {
			out.WriteString(tag)
		} else {
			out.WriteString(`<script type="` + inertType + `"`)
			if hasType {
				out.WriteString(` data-np-type="` + strings.ReplaceAll(orig, `"`, "&quot;") + `"`)
			}
			out.WriteString(attrs + ">")
		}
		if end == nil {
			out.Write(after)
			return out.Bytes()
		}
		// Le corps tel quel : un « <script » dans une chaîne JS n'est pas une balise.
		out.Write(after[:end[1]])
		rest = after[end[1]:]
	}
}

var (
	inertOpenRe = regexp.MustCompile(`(?i)<script\b[^>]*\btype\s*=\s*"` + regexp.QuoteMeta(inertType) + `"[^>]*>`)
	npTypeRe    = regexp.MustCompile(`(?i)\sdata-np-type\s*=\s*"([^"]*)"`)
)

// restoreScripts défait inertScripts dans une page reçue à l'enregistrement
// (normalement déjà fait par inject.js ; ceci couvre un client périmé).
func restoreScripts(html []byte) []byte {
	return inertOpenRe.ReplaceAllFunc(html, func(tag []byte) []byte {
		attrs := string(tag[len("<script") : len(tag)-1])
		orig := ""
		if m := npTypeRe.FindStringSubmatch(attrs); m != nil {
			orig = strings.ReplaceAll(m[1], "&quot;", `"`)
			attrs = npTypeRe.ReplaceAllString(attrs, "")
		}
		if m := typeAttrRe.FindStringSubmatchIndex(attrs); m != nil {
			attrs = attrs[:m[2]] + attrs[m[1]:]
		}
		if orig != "" {
			attrs = ` type="` + orig + `"` + attrs
		}
		return []byte("<script" + attrs + ">")
	})
}
