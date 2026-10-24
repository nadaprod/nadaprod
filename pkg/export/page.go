// Package export : sortie de production du studio de blocs — document HTML
// autonome (CSS Tailwind compilé et purgé, plus de CDN), archive ZIP
// (index.html, styles.css, assets/) et composants React/Vue.
package export

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// File : un fichier de la sortie (chemin relatif, séparateur "/").
type File struct {
	Path string
	Data []byte
}

type Block struct {
	ID   string
	Type string
	HTML string
}

// SEO : métadonnées de la page exportée. Les champs vides sont dérivés de la
// marque (voir Page.normalize).
type SEO struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	OGImage     string   `json:"og_image"` // URL absolue d'une image de partage
	URL         string   `json:"url"`      // URL canonique du site, si connue
	Lang        string   `json:"lang"`
	Langs       []string `json:"langs"` // langues cibles (une URL par langue, voir Site)
}

// Theme : la partie du design system nécessaire à l'assemblage. Les échelles
// de couleur sont calculées par le studio (une seule implémentation, côté
// navigateur) et stockées avec le projet ; on ne fait ici que les valider.
type Theme struct {
	Enabled     bool                         `json:"enabled"`
	Mode        string                       `json:"mode"`
	Primary     string                       `json:"primary"`
	FontDisplay string                       `json:"font_display"`
	FontSans    string                       `json:"font_sans"`
	Radius      string                       `json:"radius"`
	Scales      map[string]map[string]string `json:"scales"` // "primary"|"accent" → "50".."950" → #hex
}

type Brand struct {
	Name   string `json:"name"`
	Colors string `json:"colors"`
	Tone   string `json:"tone"`
}

type Page struct {
	Name   string
	Brand  Brand
	SEO    SEO
	Theme  Theme
	Blocks []Block
	// Translations : langue → i18n.Key(source) → texte traduit (voir Localized).
	Translations map[string]map[string]string
}

// Compiler : CSS Tailwind purgé pour (document, config). Fourni par pkg/sites.
type Compiler func(page []byte, cfgBody string) ([]byte, error)

var (
	hexRe    = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	familyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ]{0,48}$`)
	shadeRe  = regexp.MustCompile(`^(?:50|[1-9]00|950)$`)
	langRe   = regexp.MustCompile(`^[a-z]{2}(?:-[A-Z]{2})?$`)
)

var radiusValues = map[string]string{
	"none": "0px", "sm": "0.25rem", "md": "0.5rem", "lg": "0.75rem", "xl": "1.25rem", "full": "9999px",
}

// ---------- Normalisation ----------

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

var (
	tagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe  = regexp.MustCompile(`\s+`)
	h1Re     = regexp.MustCompile(`(?is)<h1\b[^>]*>(.*?)</h1>`)
	leadRe   = regexp.MustCompile(`(?is)</h1>.*?<p\b[^>]*>(.*?)</p>`)
	scriptRe = regexp.MustCompile(`(?is)<(script|style)\b.*?</(script|style)\s*>`)
)

func plain(fragment string) string {
	s := tagRe.ReplaceAllString(scriptRe.ReplaceAllString(fragment, " "), " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(s), " "))
}

// normalize complète le SEO : titre ← marque + promesse du hero, description
// ← premier paragraphe qui suit le <h1>. Rien n'est inventé.
func (p *Page) normalize() {
	brand := firstNonEmpty(p.Brand.Name, p.Name)
	var h1, lead string
	for _, b := range p.Blocks {
		if m := h1Re.FindStringSubmatch(b.HTML); m != nil {
			h1 = plain(m[1])
			if l := leadRe.FindStringSubmatch(b.HTML); l != nil {
				lead = plain(l[1])
			}
			break
		}
	}
	if p.SEO.Title == "" {
		if brand != "" && h1 != "" {
			p.SEO.Title = brand + " — " + h1
		} else {
			p.SEO.Title = firstNonEmpty(brand, h1, "Ma page")
		}
	}
	p.SEO.Title = clip(p.SEO.Title, 70)
	p.SEO.Description = clip(firstNonEmpty(p.SEO.Description, lead, p.Brand.Tone), 160)
	if !langRe.MatchString(p.SEO.Lang) {
		p.SEO.Lang = "fr"
	}
	p.SEO.Langs = targetLangs(p.SEO.Lang, p.SEO.Langs)
	p.SEO.URL = httpURL(p.SEO.URL)
	p.SEO.OGImage = httpURL(p.SEO.OGImage)
}

// httpURL : s si c'est une URL http(s) absolue, "" sinon.
func httpURL(s string) string {
	if u, err := url.Parse(s); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return s
}

// ---------- Design system → config Tailwind ----------

// TailwindConfig renvoie le corps « tailwind.config = {…} » de la page, ou ""
// sans design system. La config est EXÉCUTÉE par le binaire Tailwind à la
// compilation : elle est donc produite par json.Marshal à partir de valeurs
// validées une à une — jamais par concaténation de texte venu du client.
func TailwindConfig(t Theme) string {
	if !t.Enabled {
		return ""
	}
	colors := map[string]map[string]string{}
	for _, name := range []string{"primary", "accent"} {
		scale := map[string]string{}
		for shade, hex := range t.Scales[name] {
			if shadeRe.MatchString(shade) && hexRe.MatchString(hex) {
				scale[shade] = strings.ToLower(hex)
			}
		}
		if len(scale) > 0 {
			colors[name] = scale
		}
	}
	extend := map[string]any{}
	if len(colors) > 0 {
		extend["colors"] = colors
	}
	fonts := map[string][]string{}
	for key, fam := range map[string]string{"display": t.FontDisplay, "sans": t.FontSans} {
		if familyRe.MatchString(fam) {
			fonts[key] = []string{`"` + fam + `"`, "ui-sans-serif", "system-ui", "sans-serif"}
		}
	}
	if len(fonts) > 0 {
		extend["fontFamily"] = fonts
	}
	r, ok := radiusValues[t.Radius]
	if !ok {
		r = radiusValues["lg"]
	}
	extend["borderRadius"] = map[string]string{"brand": r}
	cfg, _ := json.Marshal(map[string]any{"theme": map[string]any{"extend": extend}})
	return "tailwind.config = " + string(cfg) + ";"
}

// Families : polices du thème, validées et dédoublonnées.
func (t Theme) Families() []string {
	var out []string
	for _, f := range []string{t.FontDisplay, t.FontSans} {
		if t.Enabled && familyRe.MatchString(f) && (len(out) == 0 || out[0] != f) {
			out = append(out, f)
		}
	}
	return out
}

// primary600 : la teinte 600 de l'échelle primaire du thème, "" si absente.
func (t Theme) primary600() string {
	if c := t.Scales["primary"]["600"]; t.Enabled && hexRe.MatchString(c) {
		return c
	}
	return ""
}

// ---------- Favicon ----------

func (p Page) faviconSVG() string {
	color := "#7c3aed"
	if c := p.Theme.primary600(); c != "" {
		color = c
	} else if p.Theme.Enabled && hexRe.MatchString(p.Theme.Primary) {
		color = p.Theme.Primary
	}
	letter := 'N'
	for _, r := range firstNonEmpty(p.Brand.Name, p.Name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letter = unicode.ToUpper(r)
			break
		}
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="%s"/><text x="32" y="45" font-family="system-ui,sans-serif" font-size="38" font-weight="700" fill="#fff" text-anchor="middle">%s</text></svg>`,
		color, html.EscapeString(string(letter)))
}

// ---------- Assemblage du document ----------

type docOptions struct {
	css        string // CSS compilé inline ("" ⇒ voir cssHref / CDN)
	cssHref    string // feuille externe (ZIP)
	cdn        bool   // repli : Play CDN + config inline (compilation indisponible)
	faviconRef string // href du favicon (fichier) ; "" ⇒ data URI
	fontLinks  bool   // <link> Google Fonts (document autonome sans fichiers de police)
	bodies     []string
	head, foot string // ajouts avant </head> (hreflang) et avant </body> (sélecteur de langue)
}

const weights = "400;500;600;700"

func googleFontsHref(families []string) string {
	parts := make([]string, 0, len(families))
	for _, f := range families {
		parts = append(parts, "family="+strings.ReplaceAll(f, " ", "+")+":wght@"+weights)
	}
	return "https://fonts.googleapis.com/css2?" + strings.Join(parts, "&") + "&display=swap"
}

func (p Page) document(o docOptions) []byte {
	e := html.EscapeString
	var b strings.Builder
	meta := func(attr, key, val string) {
		b.WriteString("<meta " + attr + "=\"" + key + "\" content=\"" + e(val) + "\">\n")
	}
	link := func(rel, href string) {
		b.WriteString("<link rel=\"" + rel + "\" href=\"" + e(href) + "\">\n")
	}
	b.WriteString("<!DOCTYPE html>\n<html lang=\"" + e(p.SEO.Lang) + "\">\n<head>\n")
	b.WriteString("<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n")
	b.WriteString("<title>" + e(p.SEO.Title) + "</title>\n")
	if p.SEO.Description != "" {
		meta("name", "description", p.SEO.Description)
	}
	if p.SEO.URL != "" {
		link("canonical", p.SEO.URL)
	}
	meta("property", "og:type", "website")
	meta("property", "og:title", p.SEO.Title)
	if p.SEO.Description != "" {
		meta("property", "og:description", p.SEO.Description)
	}
	if p.SEO.URL != "" {
		meta("property", "og:url", p.SEO.URL)
	}
	if name := firstNonEmpty(p.Brand.Name, p.Name); name != "" {
		meta("property", "og:site_name", name)
	}
	if p.SEO.OGImage != "" {
		meta("property", "og:image", p.SEO.OGImage)
		meta("name", "twitter:card", "summary_large_image")
	} else {
		meta("name", "twitter:card", "summary")
	}
	if c := p.Theme.primary600(); c != "" {
		meta("name", "theme-color", c)
	}
	favicon := e(o.faviconRef)
	if favicon == "" {
		favicon = "data:image/svg+xml," + url.PathEscape(p.faviconSVG())
	}
	b.WriteString("<link rel=\"icon\" type=\"image/svg+xml\" href=\"" + favicon + "\">\n")
	if fam := p.Theme.Families(); o.fontLinks && len(fam) > 0 {
		b.WriteString("<link rel=\"preconnect\" href=\"https://fonts.googleapis.com\">\n<link rel=\"preconnect\" href=\"https://fonts.gstatic.com\" crossorigin>\n")
		link("stylesheet", googleFontsHref(fam))
	}
	switch {
	case o.cdn:
		b.WriteString("<script src=\"https://cdn.tailwindcss.com\"></script>\n")
		if cfg := TailwindConfig(p.Theme); cfg != "" {
			b.WriteString("<script>" + cfg + "</script>\n")
		}
	case o.cssHref != "":
		link("stylesheet", o.cssHref)
	default:
		b.WriteString("<style>" + o.css + "</style>\n")
	}
	b.WriteString(o.head)
	bodyClass := "antialiased"
	if p.Theme.Enabled {
		bodyClass += " font-sans"
	}
	b.WriteString("</head>\n<body class=\"" + bodyClass + "\">\n")
	b.WriteString(strings.Join(o.bodies, "\n\n"))
	b.WriteString("\n" + o.foot + "</body>\n</html>\n")
	return []byte(b.String())
}

func (p Page) bodies() []string {
	out := []string{}
	for _, b := range p.Blocks {
		if h := strings.TrimSpace(b.HTML); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// compile : CSS purgé du document. Le document source passé au compilateur
// contient aussi la classe du <body> ; la config vient du thème validé.
func (p Page) compile(c Compiler, bodies []string) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("compilateur absent")
	}
	probe := p.document(docOptions{cdn: true, bodies: bodies})
	return c(probe, TailwindConfig(p.Theme))
}

// cdnWarning : avertissement du repli sur le Play CDN.
func cdnWarning(err error) string {
	return "Compilation Tailwind indisponible — la page exportée garde le CDN : " + err.Error()
}

// Result : sortie d'un export + avertissements non bloquants pour l'UI.
type Result struct {
	Files    []File
	Warnings []string
	Compiled bool
}

// HTML : un seul fichier autonome. CSS compilé inline ; les polices du thème
// passent par Google Fonts (seule option sans fichier annexe) — l'archive ZIP,
// elle, embarque les TTF. Compilation impossible ⇒ repli sur le Play CDN.
func HTML(p Page, c Compiler) Result {
	p.normalize()
	bodies := p.bodies()
	css, err := p.compile(c, bodies)
	if err != nil {
		return Result{
			Files:    []File{{"index.html", p.document(docOptions{cdn: true, fontLinks: true, bodies: bodies})}},
			Warnings: []string{cdnWarning(err)},
		}
	}
	return Result{Compiled: true, Files: []File{{"index.html", p.document(docOptions{css: string(css), fontLinks: true, bodies: bodies})}}}
}

// ---------- Site complet (ZIP / publication) ----------

// SiteOptions : dépendances du mode « site ».
type SiteOptions struct {
	Compiler   Compiler
	FontsDir   string       // miroir local (…/<Famille>/<variante>.ttf) ; "" ⇒ Google Fonts
	HTTP       *http.Client // téléchargement des images (nil ⇒ client 15 s)
	Components string       // "" | "jsx" | "vue" : ajoute components/
}

// Hôtes dont on accepte de rapatrier les images : uniquement ceux que le
// studio lui-même insère. Le serveur ne va jamais chercher une URL arbitraire
// fournie par un utilisateur (pas de SSRF).
var assetHosts = map[string]bool{"placehold.co": true, "images.pexels.com": true}

const (
	maxAssets     = 40
	maxAssetBytes = 8 << 20
)

var imgSrcRe = regexp.MustCompile(`(?i)(<img\b[^>]*?\bsrc\s*=\s*)("[^"]*"|'[^']*')`)

var fontFiles = []struct {
	file   string
	weight int
}{{"regular.ttf", 400}, {"500.ttf", 500}, {"600.ttf", 600}, {"700.ttf", 700}}

// localFonts : @font-face + fichiers TTF pour les familles présentes au miroir.
func localFonts(fontsDir string, families []string) (css string, files []File, missing []string) {
	var sb strings.Builder
	for _, fam := range families {
		found := 0
		for _, ff := range fontFiles {
			// fam est validé par familyRe (lettres, chiffres, espaces) : pas d'évasion possible.
			data, err := os.ReadFile(filepath.Join(fontsDir, fam, ff.file))
			if err != nil {
				continue
			}
			found++
			rel := "assets/fonts/" + strings.ReplaceAll(fam, " ", "-") + "/" + ff.file
			files = append(files, File{rel, data})
			sb.WriteString(fmt.Sprintf("@font-face{font-family:'%s';font-style:normal;font-weight:%d;font-display:swap;src:url('%s') format('truetype');}\n", fam, ff.weight, rel))
		}
		if found == 0 {
			missing = append(missing, fam)
		}
	}
	return sb.String(), files, missing
}

var extByType = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif", "image/svg+xml": ".svg", "image/avif": ".avif",
}

// fetchAssets rapatrie les images des hôtes connus et réécrit les src.
func fetchAssets(ctx context.Context, hc *http.Client, bodies []string) ([]string, []File, []string) {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	var files []File
	var warnings []string
	local := map[string]string{} // URL → chemin local ("" = échec mémorisé)
	out := make([]string, len(bodies))
	for i, body := range bodies {
		out[i] = imgSrcRe.ReplaceAllStringFunc(body, func(m string) string {
			sub := imgSrcRe.FindStringSubmatch(m)
			raw := html.UnescapeString(sub[2][1 : len(sub[2])-1])
			u, err := url.Parse(raw)
			if err != nil || u.Scheme != "https" || !assetHosts[strings.ToLower(u.Hostname())] {
				return m
			}
			rel, seen := local[raw]
			if !seen {
				rel = ""
				if len(files) < maxAssets {
					if data, ext, err := download(ctx, hc, raw); err == nil {
						rel = fmt.Sprintf("assets/image-%02d%s", len(files)+1, ext)
						files = append(files, File{rel, data})
					} else {
						warnings = append(warnings, "Image non rapatriée ("+u.Hostname()+") : "+err.Error())
					}
				}
				local[raw] = rel
			}
			if rel == "" {
				return m
			}
			return sub[1] + `"` + rel + `"`
		})
	}
	return out, files, warnings
}

func download(ctx context.Context, hc *http.Client, raw string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("statut %d", resp.StatusCode)
	}
	ct, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
	ct = strings.ToLower(strings.TrimSpace(ct))
	ext, ok := extByType[ct]
	if !ok {
		return nil, "", fmt.Errorf("type « %s » inattendu", ct)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxAssetBytes {
		return nil, "", fmt.Errorf("image > %d Mo", maxAssetBytes>>20)
	}
	return data, ext, nil
}

// Site : le jeu de fichiers d'un site statique complet — index.html,
// styles.css, favicon.svg, assets/ (images rapatriées, polices locales) et,
// sur demande, components/ (React ou Vue).
func Site(ctx context.Context, p Page, o SiteOptions) (Result, error) {
	raw := p // avant normalisation : chaque langue dérive son propre titre
	p.normalize()
	bodies := p.bodies()
	if len(bodies) == 0 {
		return Result{}, fmt.Errorf("la page ne contient aucun bloc généré")
	}
	res := Result{}

	// Le CSS est compilé sur les blocs d'origine : les classes ne dépendent pas des src.
	css, err := p.compile(o.Compiler, bodies)
	res.Compiled = err == nil
	if err != nil {
		res.Warnings = append(res.Warnings, cdnWarning(err))
	}

	bodies, assets, warns := fetchAssets(ctx, o.HTTP, bodies)
	res.Warnings = append(res.Warnings, warns...)

	families := p.Theme.Families()
	fontCSS, fontLinks := "", false
	if len(families) > 0 {
		var fonts []File
		var missing []string
		if o.FontsDir != "" {
			fontCSS, fonts, missing = localFonts(o.FontsDir, families)
		} else {
			missing = families
		}
		assets = append(assets, fonts...)
		if len(missing) > 0 {
			fontLinks = true // repli : ces familles restent servies par Google Fonts
			res.Warnings = append(res.Warnings, "Police(s) absente(s) du miroir local, lien Google Fonts conservé : "+strings.Join(missing, ", "))
		}
	}

	if res.Compiled {
		res.Files = append(res.Files, File{"styles.css", append([]byte(fontCSS), css...)})
	} else if fontCSS != "" {
		res.Files = append(res.Files, File{"styles.css", []byte(fontCSS)})
	}
	// Une page par langue : la source à la racine, chaque langue cible dans
	// <lang>/ — mêmes styles, favicon et assets, référencés depuis la racine.
	page := func(lp Page, root string, bodies []string, lang string) []byte {
		doc := docOptions{bodies: bodies, faviconRef: root + "favicon.svg", fontLinks: fontLinks, cdn: !res.Compiled}
		if res.Compiled {
			doc.cssHref = root + "styles.css"
		}
		doc.head, doc.foot = p.alternates(lang), p.switcher(lang)
		out := lp.document(doc)
		if !res.Compiled && fontCSS != "" {
			out = bytes.Replace(out, []byte("</head>"), []byte("<link rel=\"stylesheet\" href=\""+root+"styles.css\">\n</head>"), 1)
		}
		return out
	}
	res.Files = append(res.Files, File{"index.html", page(p, "", bodies, p.SEO.Lang)})
	local := map[string]bool{}
	for _, a := range assets {
		local[a.Path] = true
	}
	for _, lang := range p.SEO.Langs {
		lp, missing := raw.Localized(lang) // les manquants : blocs + SEO
		lp.normalize()
		lp.SEO.URL = p.langURL(lang)
		// Les corps traduits partent des corps aux images déjà rapatriées.
		lb := make([]string, len(bodies))
		for i, body := range bodies {
			t, _ := raw.apply(lang, body)
			lb[i] = subdirAssets(t, local)
		}
		if n := len(missing); n > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s : %d texte(s) non traduit(s) — source conservée", lang, n))
		}
		res.Files = append(res.Files, File{lang + "/index.html", page(lp, "../", lb, lang)})
	}
	res.Files = append(append(res.Files, File{"favicon.svg", []byte(p.faviconSVG())}), assets...)

	if o.Components != "" {
		comp, warns := components(p, bodies, o.Components)
		res.Files = append(res.Files, comp...)
		res.Warnings = append(res.Warnings, warns...)
	}
	slices.SortStableFunc(res.Files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return res, nil
}

// components : un composant par bloc + une page qui les assemble.
func components(p Page, bodies []string, kind string) ([]File, []string) {
	var files []File
	var warnings []string
	var names []string
	count := map[string]int{}
	convert := ToJSX
	if kind == "vue" {
		convert = ToVue
	}
	i := 0
	for _, b := range p.Blocks {
		if strings.TrimSpace(b.HTML) == "" {
			continue
		}
		body := bodies[i] // même ordre que p.bodies(), src d'images déjà réécrits
		i++
		count[b.Type]++
		name := ComponentName(b.Type, count[b.Type])
		src, err := convert(body, name)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Composant %s non généré : %v", name, err))
			continue
		}
		names = append(names, name)
		files = append(files, File{path.Join("components", name+"."+kind), []byte(src)})
	}
	var sb strings.Builder
	if kind == "vue" {
		sb.WriteString("<!-- Généré par NADAPROD — page assemblée (Vue 3). Importez styles.css une fois dans votre application. -->\n<template>\n")
		for _, n := range names {
			sb.WriteString("  <" + n + " />\n")
		}
		sb.WriteString("</template>\n\n<script setup>\n")
		for _, n := range names {
			sb.WriteString("import " + n + " from './" + n + ".vue';\n")
		}
		sb.WriteString("</script>\n")
		files = append(files, File{"components/Page.vue", []byte(sb.String())})
	} else {
		sb.WriteString("// Généré par NADAPROD — page assemblée (React). Importez styles.css une fois dans votre application.\n")
		for _, n := range names {
			sb.WriteString("import " + n + " from './" + n + ".jsx';\n")
		}
		sb.WriteString("\nexport default function Page() {\n  return (\n    <>\n")
		for _, n := range names {
			sb.WriteString("      <" + n + " />\n")
		}
		sb.WriteString("    </>\n  );\n}\n")
		files = append(files, File{"components/Page.jsx", []byte(sb.String())})
	}
	return files, warnings
}

// Zip : archive déterministe (ordre des fichiers, dates figées).
func Zip(files []File) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Path, Method: zip.Deflate, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var slugAccents = map[rune]rune{'à': 'a', 'â': 'a', 'ä': 'a', 'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e', 'î': 'i', 'ï': 'i',
	'ô': 'o', 'ö': 'o', 'ù': 'u', 'û': 'u', 'ü': 'u', 'ç': 'c'}

// Slug : nom de fichier sûr dérivé du nom de la page.
func Slug(name string) string {
	var sb strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if a, ok := slugAccents[r]; ok {
			r = a
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			sb.WriteRune(r)
			dash = false
		} else if !dash && sb.Len() > 0 {
			sb.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(sb.String(), "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		return "page"
	}
	return s
}
