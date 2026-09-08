package sites

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidSiteID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		// Ids réels du parc actuel.
		{"pp", true},
		{"l3dlp.com", true},
		{"theses-a-froid-v1", true},
		{"8bit-modular-clock-v1", true},
		{".agency", true}, // sites cachés
		{".family", true},
		// Refusés.
		{"", false},
		{".", false},
		{"..", false},
		{"..agency", false},
		{"-pp", false},
		{"PP", false},                      // slugify produit du minuscule
		{"a/b", false},                     // séparateur
		{"a\\b", false},                    // séparateur Windows
		{"a b", false},                     // espace
		{"<script>", false},                // hors alphabet
		{string(make([]byte, 200)), false}, // trop long
	}
	for _, c := range cases {
		if got := validSiteID(c.id); got != c.want {
			t.Errorf("validSiteID(%q) = %v, attendu %v", c.id, got, c.want)
		}
	}
}

func TestValidDomain(t *testing.T) {
	cases := []struct {
		d    string
		want bool
	}{
		{"exemple.fr", true},
		{"philippepetit.nadaprod.com", true},
		{"no.intelligences.agency", true},
		{"sub.domain.co.uk", true},
		{"xn--bcher-kva.example", true}, // punycode
		{"pp.test", true},
		{"localhost", false}, // pas de label unique
		{"exemple", false},
		{"-mauvais.fr", false},
		{"mauvais-.fr", false},
		{"exa mple.fr", false},
		{"exemple.f", false},   // TLD trop court
		{"exemple.123", false}, // TLD numérique
		{"1.2.3.4", false},     // pas d'IP
		{"<script>.fr", false},
		{"", false}, // le handler traite le vide (débranchement) avant validation
	}
	for _, c := range cases {
		if got := validDomain(c.d); got != c.want {
			t.Errorf("validDomain(%q) = %v, attendu %v", c.d, got, c.want)
		}
	}
}

// zipWith construit une archive zip en mémoire.
func zipWith(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractZipCapped(t *testing.T) {
	// 300 Ko de zéros : minuscule compressé, gros décompressé — le budget
	// compte les octets réellement écrits, pas la taille de l'archive.
	data := zipWith(t, map[string][]byte{
		"index.html": []byte("<html></html>"),
		"gros.bin":   make([]byte, 300<<10),
	})

	budget := int64(100 << 10) // 100 Ko
	err := extractZip(data, t.TempDir(), &budget)
	if !errors.Is(err, errArchiveTooLarge) {
		t.Fatalf("extractZip avec budget dépassé : err = %v, attendu errArchiveTooLarge", err)
	}

	// Le même contenu passe avec un budget suffisant.
	dest := t.TempDir()
	budget = int64(1 << 20) // 1 Mo
	if err := extractZip(data, dest, &budget); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "gros.bin")); err != nil {
		t.Fatal(err)
	}
	if rest := int64(1<<20) - budget; rest < 300<<10 {
		t.Errorf("budget décompté = %d octets, attendu ≥ %d", rest, 300<<10)
	}
}

func TestExtractZipSlip(t *testing.T) {
	// Contrat de safeJoin : les « ../ » sont neutralisés (path.Clean ancré à
	// la racine), le fichier atterrit DANS dest — jamais au-dessus.
	data := zipWith(t, map[string][]byte{"../evil.html": []byte("pwned")})
	parent := t.TempDir()
	dest := filepath.Join(parent, "dev")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	budget := int64(maxExtractBytes)
	if err := extractZip(data, dest, &budget); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, "evil.html")); err == nil {
		t.Fatal("le fichier a échappé au dossier d'extraction")
	}
	if _, err := os.Stat(filepath.Join(dest, "evil.html")); err != nil {
		t.Fatal("le fichier neutralisé devrait être écrit sous dest :", err)
	}
}
