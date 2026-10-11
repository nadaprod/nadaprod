package sites

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Pages d'un site : ce dont le studio de blocs a besoin pour naître d'une page
// existante (ReadPage) ou pour déposer son résultat dans un site (SavePage),
// et ce que /sites propose directement (site vide, copie de page).

var (
	ErrPageExists   = errors.New("cette page existe déjà")
	ErrPageNotFound = errors.New("page introuvable")
)

// pageRel valide un chemin de page : relatif, .html, sans échappement.
func pageRel(rel string) (string, error) {
	rel = strings.TrimPrefix(strings.TrimSpace(rel), "/")
	rel = path.Clean(rel)
	if rel == "." || rel == "" || !strings.HasSuffix(strings.ToLower(rel), ".html") ||
		strings.HasPrefix(rel, "../") || strings.Contains(rel, "\\") {
		return "", fmt.Errorf("chemin de page invalide (attendu : dossier/nom.html)")
	}
	return rel, nil
}

// PageName : un nom saisi (« À propos ») devient un chemin sûr (« a-propos.html »).
func PageName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(strings.ToLower(name), ".html") {
		name = name[:len(name)-5]
	}
	return slugify(unaccent(name)) + ".html"
}

// unaccent : les lettres accentuées du français retombent sur leur base
// (slugify ne connaît que l'ASCII).
var unaccent = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "À", "A", "Â", "A", "Ä", "A",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "É", "E", "È", "E", "Ê", "E", "Ë", "E",
	"î", "i", "ï", "i", "Î", "I", "Ï", "I", "ô", "o", "ö", "o", "Ô", "O", "Ö", "O",
	"ù", "u", "û", "u", "ü", "u", "Ù", "U", "Û", "U", "Ü", "U", "ç", "c", "Ç", "C", "œ", "oe", "æ", "ae",
).Replace

// CreateSite crée un site vide (dev/ sans page) sous un identifiant dérivé du
// nom, suffixé s'il est pris. Le studio y déposera sa première page.
func (s *Store) CreateSite(name string) (string, error) {
	base := slugify(name)
	if !validSiteID(base) {
		return "", fmt.Errorf("nom de site invalide")
	}
	return s.newSite(base)
}

// pagePath valide un couple (site, page) et résout la page dans la source.
func (s *Store) pagePath(id, rel string) (string, string, error) {
	if !validSiteID(id) {
		return "", "", fmt.Errorf("site invalide")
	}
	rel, err := pageRel(rel)
	if err != nil {
		return "", "", err
	}
	full, err := safeJoin(s.srcDir(id), rel)
	return rel, full, err
}

// ReadPage : la source d'une page (dev/), telle que l'éditeur la lit.
func (s *Store) ReadPage(id, rel string) ([]byte, error) {
	_, p, err := s.pagePath(id, rel)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrPageNotFound
	}
	return b, err
}

// SavePage écrit une page dans dev/ (dossiers créés au besoin) et la
// republie en arrière-plan. Sans overwrite, une page existante est protégée.
func (s *Store) SavePage(id, rel string, html []byte, overwrite bool) (string, error) {
	rel, target, err := s.pagePath(id, rel)
	if err != nil {
		return "", err
	}
	if !dirExists(s.siteDir(id)) {
		return "", fmt.Errorf("site introuvable")
	}
	if _, err := os.Stat(target); err == nil && !overwrite {
		return "", ErrPageExists
	}
	if err := os.MkdirAll(path.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, html, 0o644); err != nil {
		return "", err
	}
	s.builds.Add(1)
	go func() {
		defer s.builds.Done()
		s.RebuildPage(id, rel)
	}()
	return rel, nil
}

// waitBuilds attend les republications lancées par SavePage (tests).
func (s *Store) waitBuilds() { s.builds.Wait() }

// CopyPage duplique une page du site sous un nouveau chemin.
func (s *Store) CopyPage(id, from, to string) (string, error) {
	src, err := s.ReadPage(id, from)
	if err != nil {
		return "", err
	}
	return s.SavePage(id, to, src, false)
}

// BlankPage : la page minimale d'un site créé de zéro — Tailwind via CDN,
// comme toute source de dev/ (le pipeline compile à la publication).
func BlankPage(title string) []byte {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Nouvelle page"
	}
	title = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(title)
	return []byte(`<!DOCTYPE html>
<html lang="fr">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>` + title + `</title>
<script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="antialiased">
<main class="min-h-screen grid place-items-center p-8 text-center">
  <h1 class="text-3xl font-bold tracking-tight">` + title + `</h1>
</main>
</body>
</html>
`)
}
