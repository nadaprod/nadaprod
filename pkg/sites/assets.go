package sites

import (
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Assets d'une page : ce que l'atelier de code ouvre en onglets à côté du
// HTML — les feuilles de style et scripts LOCAUX que la page référence.

// Extensions qu'on accepte d'éditer et d'enregistrer comme texte. Tout le
// reste (images, polices, archives) n'a rien à faire dans un éditeur.
var editableExt = map[string]bool{
	".html": true, ".htm": true, ".css": true, ".js": true, ".mjs": true, ".json": true,
	".svg": true, ".txt": true, ".md": true, ".xml": true, ".webmanifest": true,
}

// Editable : ce chemin désigne-t-il un fichier texte éditable ?
func Editable(rel string) bool { return editableExt[strings.ToLower(path.Ext(rel))] }

var (
	// <link … rel="stylesheet" … href="…"> (attributs dans n'importe quel ordre)
	linkTagRe = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	relAttrRe = regexp.MustCompile(`(?is)\brel\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	hrefRe    = regexp.MustCompile(`(?is)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	// <script … src="…">
	scriptSrcRe = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))[^>]*>`)
)

func pick(m []string) string {
	for _, v := range m[1:] {
		if v != "" {
			return v
		}
	}
	return ""
}

// localRef résout une URL trouvée dans la page en chemin relatif à la racine
// du site, ou "" si elle n'est pas locale (http(s), //cdn, data:, mailto…).
func localRef(raw, page string) string {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "//") || strings.Contains(raw, ":") {
		return ""
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	var rel string
	if strings.HasPrefix(raw, "/") {
		rel = path.Clean(raw)
	} else {
		rel = path.Clean(path.Join(path.Dir("/"+page), raw))
	}
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || rel == ".." {
		return ""
	}
	return rel
}

// PageAssets extrait, dans l'ordre d'apparition et sans doublon, les CSS et
// JS locaux référencés par une page (chemins relatifs à la racine du site).
func PageAssets(raw []byte, page string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(ref string, ext ...string) {
		rel := localRef(ref, page)
		if rel != "" && !seen[rel] && slices.Contains(ext, strings.ToLower(path.Ext(rel))) {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	for _, tag := range linkTagRe.FindAll(raw, -1) {
		rel := relAttrRe.FindStringSubmatch(string(tag))
		if rel == nil || !strings.Contains(strings.ToLower(pick(rel)), "stylesheet") {
			continue
		}
		if m := hrefRe.FindStringSubmatch(string(tag)); m != nil {
			add(pick(m), ".css")
		}
	}
	for _, m := range scriptSrcRe.FindAllStringSubmatch(string(raw), -1) {
		add(pick(m), ".js", ".mjs")
	}
	return out
}

// codeExt : les assets que l'atelier ouvre en onglets.
var codeExt = []string{".css", ".js", ".mjs"}

// Asset : un fichier texte du site tel que l'atelier le liste.
type Asset struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Assets d'une page : ceux qu'elle référence (et qui existent), puis les
// autres CSS/JS du site, pour les ouvrir d'un clic.
func (s *Store) Assets(id, page string) (referenced, others []Asset, err error) {
	raw, err := s.ReadPage(id, page)
	if err != nil {
		return nil, nil, err
	}
	root := s.srcDir(id)
	referenced, others = []Asset{}, []Asset{}
	seen := map[string]bool{}
	for _, rel := range PageAssets(raw, page) {
		p, err := safeJoin(root, rel)
		if err != nil {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			referenced = append(referenced, Asset{rel, st.Size()})
			seen[rel] = true
		}
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if slices.Contains(codeExt, strings.ToLower(path.Ext(rel))) && !seen[rel] && !strings.HasPrefix(rel, "np-fonts/") {
			if st, err := d.Info(); err == nil {
				others = append(others, Asset{rel, st.Size()})
			}
		}
		return nil
	})
	sort.Slice(others, func(i, j int) bool { return others[i].Path < others[j].Path })
	return referenced, others, nil
}
