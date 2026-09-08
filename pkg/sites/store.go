package sites

import (
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Package sites — les sites importés. Un site = un dossier, le filesystem
// est la base de données :
//
//	sites/<id>/site.json   → { "domain": "exemple.com", "compile": true, … }
//	sites/<id>/dev/…       → la source éditable (uploadée, retouchée)
//	sites/<id>/public/…    → la sortie publiée (build.go), régénérable
//
// Le service sur domaine essaie public/ puis retombe sur dev/ : seuls les
// fichiers transformés sont dupliqués, jamais les assets.

type Meta struct {
	Domain string `json:"domain"`
	// Réglages de publication (nil = activé — le défaut « zéro effort »).
	Compile *bool `json:"compile,omitempty"` // Tailwind CDN → CSS compilé
	PWA     *bool `json:"pwa,omitempty"`     // manifest + service worker générés
	GDPR    *bool `json:"gdpr,omitempty"`    // bandeau « tout est local »
	Fonts   *bool `json:"fonts,omitempty"`   // Google Fonts → polices servies par le site
}

func onByDefault(p *bool) bool { return p == nil || *p }
func (m Meta) CompileOn() bool { return onByDefault(m.Compile) }
func (m Meta) PWAOn() bool     { return onByDefault(m.PWA) }
func (m Meta) GDPROn() bool    { return onByDefault(m.GDPR) }
func (m Meta) FontsOn() bool   { return onByDefault(m.Fonts) }

type Store struct {
	root       string
	twBin      string // binaire standalone Tailwind ("" = compilation désactivée)
	fontsDir   string // miroir local Google Fonts ("" = polices locales désactivées)
	gdprTag    string // bandeau RGPD injecté, traductions embarquées (build.go)
	mu         sync.RWMutex
	domains    map[string]string // host → siteID
	buildLocks sync.Map          // siteID → *sync.Mutex (build.go)
}

func NewStore(root, twBin, fontsDir, i18nDir string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if fontsDir != "" {
		// safeJoin compare des préfixes : la base doit être un chemin propre
		// (« ./web/webfonts » ne matcherait jamais un filepath.Join nettoyé).
		fontsDir = filepath.Clean(fontsDir)
	}
	s := &Store{root: root, twBin: twBin, fontsDir: fontsDir,
		gdprTag: buildGDPRTag(gdprTranslations(i18nDir)), domains: map[string]string{}}
	s.migrateLayout()
	return s, s.reloadDomains()
}

// migrateLayout : les sites d'avant le pipeline de build n'ont qu'un public/.
// On en fait la source (dev/) ; public/ sera régénéré par BuildAll.
func (s *Store) migrateLayout() {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dev, pub := s.devDir(e.Name()), s.publicDir(e.Name())
		if _, err := os.Stat(dev); !os.IsNotExist(err) {
			continue
		}
		if _, err := os.Stat(pub); err == nil {
			if err := os.Rename(pub, dev); err != nil {
				log.Printf("migration %s : public/ → dev/ impossible : %v", e.Name(), err)
			}
		}
	}
}

func (s *Store) siteDir(id string) string   { return filepath.Join(s.root, id) }
func (s *Store) devDir(id string) string    { return filepath.Join(s.root, id, "dev") }
func (s *Store) publicDir(id string) string { return filepath.Join(s.root, id, "public") }

// srcDir : la source éditable. dev/ après migration ; repli sur public/ pour
// un site legacy dont la migration aurait échoué (on n'y écrit alors jamais
// de build par-dessus — voir BuildSite).
func (s *Store) srcDir(id string) string {
	if d := s.devDir(id); dirExists(d) {
		return d
	}
	return s.publicDir(id)
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func (s *Store) reloadDomains() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	m := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var meta Meta
		if b, err := os.ReadFile(filepath.Join(s.root, e.Name(), "site.json")); err == nil {
			_ = json.Unmarshal(b, &meta)
		}
		if meta.Domain != "" {
			m[strings.ToLower(meta.Domain)] = e.Name()
		}
	}
	s.mu.Lock()
	s.domains = m
	s.mu.Unlock()
	return nil
}

func (s *Store) meta(id string) Meta {
	var meta Meta
	if b, err := os.ReadFile(filepath.Join(s.siteDir(id), "site.json")); err == nil {
		_ = json.Unmarshal(b, &meta)
	}
	return meta
}

func (s *Store) writeMeta(id string, meta Meta) error {
	b, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(filepath.Join(s.siteDir(id), "site.json"), b, 0o644)
}

// listPages renvoie les chemins relatifs de tous les *.html de la source.
func (s *Store) listPages(id string) ([]string, error) {
	root := s.srcDir(id)
	var pages []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".html") {
			rel, _ := filepath.Rel(root, p)
			pages = append(pages, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Slice(pages, func(i, j int) bool { // index.html d'abord, puis alpha
		if (pages[i] == "index.html") != (pages[j] == "index.html") {
			return pages[i] == "index.html"
		}
		return pages[i] < pages[j]
	})
	return pages, err
}
