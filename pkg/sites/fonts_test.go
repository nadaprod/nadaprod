package sites

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseGoogleFonts(t *testing.T) {
	cases := []struct {
		name string
		href string
		want []fontRequest
	}{
		{
			"css2 multi-familles avec graisses",
			"https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@500;700&family=Inter&display=swap",
			[]fontRequest{
				{Family: "Space Grotesk", Variants: []fontVariant{{Weight: 500}, {Weight: 700}}},
				{Family: "Inter", Variants: []fontVariant{{Weight: 400}}},
			},
		},
		{
			"css2 ital,wght",
			"https://fonts.googleapis.com/css2?family=Lora:ital,wght@0,400;1,700",
			[]fontRequest{
				{Family: "Lora", Variants: []fontVariant{{Weight: 400}, {Weight: 700, Italic: true}}},
			},
		},
		{
			"css2 plage variable développée",
			"https://fonts.googleapis.com/css2?family=Inter:wght@150..400",
			[]fontRequest{
				{Family: "Inter", Variants: []fontVariant{{Weight: 200}, {Weight: 300}, {Weight: 400}}},
			},
		},
		{
			"amp encodé en entité HTML (cas réel dans les pages)",
			"https://fonts.googleapis.com/css2?family=Inter:wght@400&amp;family=Lora",
			[]fontRequest{
				{Family: "Inter", Variants: []fontVariant{{Weight: 400}}},
				{Family: "Lora", Variants: []fontVariant{{Weight: 400}}},
			},
		},
		{
			"API legacy /css avec | et jetons",
			"https://fonts.googleapis.com/css?family=Roboto:400,700italic,bold|Open+Sans",
			[]fontRequest{
				{Family: "Roboto", Variants: []fontVariant{{Weight: 400}, {Weight: 700, Italic: true}, {Weight: 700}}},
				{Family: "Open Sans", Variants: []fontVariant{{Weight: 400}}},
			},
		},
	}
	for _, c := range cases {
		if got := parseGoogleFonts(c.href); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s :\n  got  %+v\n  want %+v", c.name, got, c.want)
		}
	}
}

func TestVariantFile(t *testing.T) {
	cases := []struct {
		v    fontVariant
		want string
	}{
		{fontVariant{Weight: 400}, "regular.ttf"},
		{fontVariant{Weight: 400, Italic: true}, "italic.ttf"},
		{fontVariant{Weight: 500}, "500.ttf"},
		{fontVariant{Weight: 700, Italic: true}, "700italic.ttf"},
	}
	for _, c := range cases {
		if got := variantFile(c.v); got != c.want {
			t.Errorf("variantFile(%+v) = %q, attendu %q", c.v, got, c.want)
		}
	}
}

// fontsFixture crée un mini-miroir : Inter (regular, 700) et Lora (regular).
func fontsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"Inter/regular.ttf", "Inter/700.ttf", "Lora/regular.ttf"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("ttf"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLocalizeFonts(t *testing.T) {
	s := &Store{root: t.TempDir(), fontsDir: fontsFixture(t)}

	page := []byte(`<head>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;700&amp;display=swap" rel="stylesheet">
</head>`)
	out, changed := s.localizeFonts("demo", page)
	if !changed {
		t.Fatal("réécriture attendue")
	}
	got := string(out)
	if strings.Contains(got, "fonts.googleapis.com") || strings.Contains(got, "fonts.gstatic.com") {
		t.Errorf("références Google restantes :\n%s", got)
	}
	for _, frag := range []string{"data-np-fonts", "font-weight:400", "font-weight:700", "/np-fonts/Inter/regular.ttf", "/np-fonts/Inter/700.ttf"} {
		if !strings.Contains(got, frag) {
			t.Errorf("fragment manquant %q dans :\n%s", frag, got)
		}
	}
	for _, f := range []string{"Inter/regular.ttf", "Inter/700.ttf"} {
		if _, err := os.Stat(filepath.Join(s.publicDir("demo"), "np-fonts", f)); err != nil {
			t.Errorf("TTF non copié : %s", f)
		}
	}
}

func TestLocalizeFontsFamilleAbsente(t *testing.T) {
	s := &Store{root: t.TempDir(), fontsDir: fontsFixture(t)}

	// Deux liens : « Nope » est inconnue ⇒ son lien reste, et les preconnect
	// aussi (il reste un lien Google) ; « Lora » est localisée.
	page := []byte(`<head>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Nope" rel="stylesheet">
<link href="https://fonts.googleapis.com/css2?family=Lora" rel="stylesheet">
</head>`)
	out, changed := s.localizeFonts("demo", page)
	if !changed {
		t.Fatal("réécriture partielle attendue")
	}
	got := string(out)
	if !strings.Contains(got, "family=Nope") {
		t.Error("le lien de la famille absente aurait dû être conservé")
	}
	if !strings.Contains(got, "/np-fonts/Lora/regular.ttf") {
		t.Error("Lora aurait dû être localisée")
	}
	if !strings.Contains(got, `rel="preconnect"`) {
		t.Error("preconnect conservé attendu tant qu'un lien Google subsiste")
	}
}

func TestLocalizeFontsVarianteManquante(t *testing.T) {
	s := &Store{root: t.TempDir(), fontsDir: fontsFixture(t)}

	// Lora n'a que regular.ttf : la graisse 900 est abandonnée, 400 suffit.
	page := []byte(`<link href="https://fonts.googleapis.com/css2?family=Lora:wght@400;900" rel="stylesheet">`)
	out, changed := s.localizeFonts("demo", page)
	if !changed {
		t.Fatal("réécriture attendue")
	}
	got := string(out)
	if strings.Contains(got, "900.ttf") {
		t.Error("variante absente du miroir référencée à tort")
	}
	if !strings.Contains(got, "/np-fonts/Lora/regular.ttf") {
		t.Error("la variante disponible aurait dû être servie")
	}
}
