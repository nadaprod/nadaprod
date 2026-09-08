package sites

import (
	"fmt"
	"html"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	// <link href="https://fonts.googleapis.com/css2?family=…"> (ou l'API
	// legacy /css?family=…) — la feuille de styles Google Fonts d'une page.
	gfLinkRe = regexp.MustCompile(`(?i)<link[^>]*\bhref\s*=\s*["'](https://fonts\.googleapis\.com/css2?\?[^"']+)["'][^>]*>`)
	// Les préconnexions associées, à retirer quand plus aucun lien ne reste.
	gfPreconnectRe = regexp.MustCompile(`(?i)\s*<link[^>]*\bhref\s*=\s*["']https://fonts\.g(?:oogleapis|static)\.com/?["'][^>]*>`)
)

// ---------- Polices locales (miroir Google Fonts) ----------

type fontVariant struct {
	Weight int
	Italic bool
}

type fontRequest struct {
	Family   string
	Variants []fontVariant
}

// parseGoogleFonts décode une URL fonts.googleapis.com (/css2 moderne ou /css
// legacy) en familles + variantes demandées. Les axes autres que ital/wght
// (opsz…) sont ignorés ; une plage variable « 100..900 » est développée sur
// les graisses standard.
func parseGoogleFonts(href string) []fontRequest {
	u, err := url.Parse(html.UnescapeString(href))
	if err != nil {
		return nil
	}
	css2 := strings.HasSuffix(u.Path, "/css2")
	// Pas de u.Query() : depuis Go 1.17 elle rejette les « ; », omniprésents
	// dans la syntaxe css2 (wght@400;700). On découpe uniquement sur « & ».
	var families []string
	for _, kv := range strings.Split(u.RawQuery, "&") {
		k, v, _ := strings.Cut(kv, "=")
		if k != "family" {
			continue
		}
		if spec, err := url.QueryUnescape(v); err == nil {
			families = append(families, spec)
		}
	}
	var out []fontRequest
	for _, spec := range families {
		if css2 {
			if req := parseCSS2Family(spec); req.Family != "" {
				out = append(out, req)
			}
			continue
		}
		for _, one := range strings.Split(spec, "|") { // legacy : familles séparées par |
			if req := parseLegacyFamily(one); req.Family != "" {
				out = append(out, req)
			}
		}
	}
	return out
}

// parseCSS2Family : « Name », « Name:wght@500;700 », « Name:ital,wght@0,400;1,700 ».
func parseCSS2Family(spec string) fontRequest {
	name, axesPart, hasAxes := strings.Cut(spec, ":")
	req := fontRequest{Family: strings.TrimSpace(name)}
	axesStr, tuples, hasTuples := strings.Cut(axesPart, "@")
	if !hasAxes || !hasTuples {
		req.Variants = []fontVariant{{Weight: 400}}
		return req
	}
	axes := strings.Split(axesStr, ",")
	for _, tuple := range strings.Split(tuples, ";") {
		vals := strings.Split(tuple, ",")
		italic, weights := false, []int{400}
		for i, axis := range axes {
			if i >= len(vals) {
				break
			}
			switch axis {
			case "ital":
				italic = vals[i] == "1"
			case "wght":
				if lo, hi, isRange := strings.Cut(vals[i], ".."); isRange {
					l, err1 := strconv.Atoi(lo)
					h, err2 := strconv.Atoi(hi)
					if err1 == nil && err2 == nil {
						weights = weights[:0]
						for w := (l + 99) / 100 * 100; w <= h; w += 100 {
							weights = append(weights, w)
						}
					}
				} else if w, err := strconv.Atoi(vals[i]); err == nil {
					weights = []int{w}
				}
			}
		}
		for _, w := range weights {
			req.Variants = appendVariant(req.Variants, fontVariant{Weight: w, Italic: italic})
		}
	}
	if len(req.Variants) == 0 {
		req.Variants = []fontVariant{{Weight: 400}}
	}
	return req
}

// parseLegacyFamily : « Name:400,700italic,bold » (API /css d'origine).
func parseLegacyFamily(spec string) fontRequest {
	name, tokens, hasTokens := strings.Cut(spec, ":")
	req := fontRequest{Family: strings.TrimSpace(name)}
	if !hasTokens {
		req.Variants = []fontVariant{{Weight: 400}}
		return req
	}
	for _, t := range strings.Split(tokens, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		v := fontVariant{Weight: 400}
		switch t {
		case "", "regular", "normal":
		case "italic":
			v.Italic = true
		case "bold":
			v.Weight = 700
		case "bolditalic":
			v.Weight, v.Italic = 700, true
		default:
			if strings.HasSuffix(t, "italic") {
				v.Italic = true
				t = strings.TrimSuffix(t, "italic")
			} else if strings.HasSuffix(t, "i") {
				v.Italic = true
				t = strings.TrimSuffix(t, "i")
			}
			w, err := strconv.Atoi(t)
			if err != nil {
				continue // jeton inconnu (subset…) : ignoré
			}
			v.Weight = w
		}
		req.Variants = appendVariant(req.Variants, v)
	}
	if len(req.Variants) == 0 {
		req.Variants = []fontVariant{{Weight: 400}}
	}
	return req
}

func appendVariant(list []fontVariant, v fontVariant) []fontVariant {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// variantFile : convention du miroir (regular.ttf, 500.ttf, 700italic.ttf…).
func variantFile(v fontVariant) string {
	switch {
	case v.Weight == 400 && !v.Italic:
		return "regular.ttf"
	case v.Weight == 400 && v.Italic:
		return "italic.ttf"
	case v.Italic:
		return fmt.Sprintf("%ditalic.ttf", v.Weight)
	default:
		return fmt.Sprintf("%d.ttf", v.Weight)
	}
}

func fontFaceCSS(family string, v fontVariant, file string) string {
	style := "normal"
	if v.Italic {
		style = "italic"
	}
	dir := strings.ReplaceAll(family, " ", "%20")
	return fmt.Sprintf("@font-face{font-family:'%s';font-style:%s;font-weight:%d;font-display:swap;src:url('/np-fonts/%s/%s') format('truetype');}",
		family, style, v.Weight, dir, file)
}

// localizeFonts remplace chaque <link> Google Fonts par un <style> @font-face
// local et copie les TTF nécessaires dans public/np-fonts/. Une famille ou
// toutes les variantes absentes du miroir ⇒ le lien d'origine est conservé
// (dégradation douce, par lien). Quand plus aucun lien Google ne reste, les
// <link rel="preconnect"> vers fonts.g* sont retirés.
func (s *Store) localizeFonts(id string, raw []byte) ([]byte, bool) {
	changed := false
	out := gfLinkRe.ReplaceAllFunc(raw, func(tag []byte) []byte {
		href := gfLinkRe.FindSubmatch(tag)[1]
		reqs := parseGoogleFonts(string(href))
		if len(reqs) == 0 {
			return tag
		}
		type copyJob struct{ src, dst string }
		var css strings.Builder
		var jobs []copyJob
		for _, req := range reqs {
			famDir, err := safeJoin(s.fontsDir, req.Family)
			if err != nil || !dirExists(famDir) {
				log.Printf("build %s : police « %s » absente du miroir — lien Google conservé", id, req.Family)
				return tag
			}
			kept := 0
			for _, v := range req.Variants {
				file := variantFile(v)
				src := filepath.Join(famDir, file)
				if _, err := os.Stat(src); err != nil {
					continue // variante absente : les autres suffisent
				}
				kept++
				css.WriteString(fontFaceCSS(req.Family, v, file))
				jobs = append(jobs, copyJob{src, filepath.Join(s.publicDir(id), "np-fonts", req.Family, file)})
			}
			if kept == 0 {
				log.Printf("build %s : aucune variante de « %s » dans le miroir — lien Google conservé", id, req.Family)
				return tag
			}
		}
		for _, j := range jobs {
			if _, err := os.Stat(j.dst); err == nil {
				continue // déjà copié (page précédente du même build)
			}
			f, err := os.Open(j.src)
			if err != nil {
				log.Printf("build %s : lecture de %s : %v", id, j.src, err)
				return tag
			}
			err = writeFile(j.dst, f)
			f.Close()
			if err != nil {
				log.Printf("build %s : copie de %s : %v", id, j.src, err)
				return tag
			}
		}
		changed = true
		return []byte("<style data-np-fonts>" + css.String() + "</style>")
	})
	if changed && !gfLinkRe.Match(out) {
		out = gfPreconnectRe.ReplaceAll(out, nil)
	}
	return out, changed
}
