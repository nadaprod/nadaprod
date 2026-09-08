package sites

import (
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// siteIDRe : les ids effectivement produits (slugify) ou créés à la main
// (sites cachés « .famille », « l3dlp.com ») — jamais « . », « .. » ni de
// séparateur. Complète safeJoin sur toutes les routes :id.
var siteIDRe = regexp.MustCompile(`^\.?[a-z0-9][a-z0-9._-]*$`)

func validSiteID(id string) bool { return len(id) <= 128 && siteIDRe.MatchString(id) }

// domainRe : nom d'hôte complet uniquement (labels alphanumériques avec
// tirets internes, TLD alphabétique) — pas d'IP, pas de « localhost ».
var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func validDomain(d string) bool { return len(d) <= 253 && domainRe.MatchString(d) }

// safeJoin résout p sous base et refuse toute évasion (zip-slip, ../…).
func safeJoin(base, p string) (string, error) {
	p = filepath.FromSlash(path.Clean("/" + strings.ReplaceAll(p, "\\", "/")))
	full := filepath.Join(base, p)
	if full != base && !strings.HasPrefix(full, base+string(filepath.Separator)) {
		return "", errors.New("chemin hors du site")
	}
	return full, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	for _, ext := range []string{".zip", ".tar.gz", ".tgz"} {
		s = strings.TrimSuffix(s, ext)
	}
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-")
	if s == "" {
		s = "site"
	}
	return s
}
