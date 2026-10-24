package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"nadaprod/pkg/sites"
)

// siteStore : le stockage des sites, avec la même configuration de
// publication pour le serveur et pour « build-site ».
func siteStore() (*sites.Store, error) {
	// Binaire standalone Tailwind (compilation locale des sites publiés).
	// Absent ⇒ dégradation douce : les pages publiées gardent le CDN.
	twBin := envOr("TAILWIND_BIN", "./bin/tailwindcss")
	if _, err := os.Stat(twBin); err != nil {
		log.Printf("tailwindcss introuvable (%s) — compilation locale désactivée, CDN conservé", twBin)
		twBin = ""
	}

	// Miroir local Google Fonts (servi sur /webfonts, réutilisé par le build).
	// Absent ⇒ dégradation douce : les sites publiés gardent leurs liens Google.
	fontsDir := envOr("WEBFONTS_DIR", "./web/webfonts")
	if st, err := os.Stat(fontsDir); err != nil || !st.IsDir() {
		log.Printf("webfonts introuvable (%s) — polices locales désactivées sur les sites publiés", fontsDir)
		fontsDir = ""
	}

	// Les traductions du bandeau RGPD injecté viennent des mêmes JSON que l'UI.
	return sites.NewStore(envOr("SITES_DIR", "./sites"), twBin, fontsDir, "./web/static/i18n")
}

// buildSites reconstruit public/ depuis dev/ pour chaque site, de façon
// synchrone (BuildSite journalise les pages publiées). Code de sortie non nul
// si un identifiant ne désigne pas un site.
func buildSites(ids []string) int {
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "usage : nadaprod build-site <id>…   (ex. nadaprod build-site .art)")
		return 2
	}
	store, err := siteStore()
	if err != nil {
		log.Printf("initialisation du stockage sites : %v", err)
		return 1
	}
	root := envOr("SITES_DIR", "./sites")
	code := 0
	for _, id := range ids {
		if st, err := os.Stat(filepath.Join(root, id, "dev")); id == "" || filepath.Base(id) != id || err != nil || !st.IsDir() {
			log.Printf("build %s : pas de site avec un dev/ sous %s", id, root)
			code = 1
			continue
		}
		store.BuildSite(id)
	}
	return code
}
