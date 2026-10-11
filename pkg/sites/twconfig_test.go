package sites

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseTailwindConfigAccepts(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"vide", "  \n ", "{}"},
		{"objet vide", "tailwind.config = {}", "{}"},
		{"point-virgule et blancs", "\n  tailwind.config={ darkMode : 'class' } ;\n", `{"darkMode":"class"}`},
		{"clés non citées, guillemets simples, virgules finales (cas réel)",
			"tailwind.config = {\n theme: {\n  extend: {\n   fontFamily: {\n    mono: ['\"JetBrains Mono\"', 'monospace'],\n    sans: ['\"Inter\"', 'sans-serif'],\n   },\n   colors: {\n    acid: '#CCFF00', // signal\n    void: '#050505',\n   },\n  }\n }\n}",
			`{"theme":{"extend":{"fontFamily":{"mono":["\"JetBrains Mono\"","monospace"],"sans":["\"Inter\"","sans-serif"]},"colors":{"acid":"#CCFF00","void":"#050505"}}}}`},
		{"clés citées avec tirets, commentaire bloc", "/* thème */ tailwind.config = { theme: { extend: { colors: { 'mod-black': \"#0a0a0a\", /* accent */ 'mod-accent': '#008ECC' } } } }",
			`{"theme":{"extend":{"colors":{"mod-black":"#0a0a0a","mod-accent":"#008ECC"}}}}`},
		{"// dans une chaîne n'est pas un commentaire", "tailwind.config = { theme: { extend: { backgroundImage: { hero: \"url('https://example.test/a.png')\" } } } }",
			`{"theme":{"extend":{"backgroundImage":{"hero":"url('https://example.test/a.png')"}}}}`},
		{"nombres, booléens, null, tableaux imbriqués", "tailwind.config = { important: true, corePlugins: { preflight: false }, x: null, theme: { extend: { spacing: { 128: '32rem', '0.5': 2, neg: -1.5, e: 1e3, h: 0x10, d: .5 } }, n: [[1, 2], []] } }",
			`{"important":true,"corePlugins":{"preflight":false},"x":null,"theme":{"extend":{"spacing":{"128":"32rem","0.5":2,"neg":-1.5,"e":1000,"h":16,"d":0.5}},"n":[[1,2],[]]}}`},
		{"ordre des clés conservé (screens)", "tailwind.config = { theme: { screens: { xs: '360px', sm: '640px', md: '768px', lg: '1024px' } } }",
			`{"theme":{"screens":{"xs":"360px","sm":"640px","md":"768px","lg":"1024px"}}}`},
		{"échappements", `tailwind.config = { a: 'l\'apostrophe', b: "guille\"met", c: 'tab\tfin', d: '\u00e9\x41' }`,
			`{"a":"l'apostrophe","b":"guille\"met","c":"tab\tfin","d":"éA"}`},
		{"unicode dans les valeurs", "tailwind.config = { theme: { extend: { fontFamily: { titre: ['Bébas Néue', 'sans-serif'] } } } }",
			`{"theme":{"extend":{"fontFamily":{"titre":["Bébas Néue","sans-serif"]}}}}`},
		{"paire de substituts recombinée, moitié isolée tolérée", "tailwind.config = { a: '" + `\` + "uD83D" + `\` + "uDE00', b: '" + `\` + "uD83Dx' }",
			`{"a":"😀","b":"` + "\uFFFD" + `x"}`},
		{"continuations de ligne (LF, CRLF, CR, U+2028)", "tailwind.config = { a: 'ur" + `\` + "\nl', b: 'ur" + `\` + "\r\nl', c: 'ur" + `\` + "\rl', d: 'ur" + `\` + "\u2028l' }",
			`{"a":"url","b":"url","c":"url","d":"url"}`},
		{"clés numériques nommées comme en JS", "tailwind.config = { theme: { zIndex: { 1000000: 'a', 123456789: 'b', 0.000001: 'c', 1e-7: 'd', 1e21: 'e', 1.5: 'f' } } }",
			`{"theme":{"zIndex":{"1000000":"a","123456789":"b","0.000001":"c","1e-7":"d","1e+21":"e","1.5":"f"}}}`},
		{"caractères < et & (échappés : le JSON reste inerte même collé dans du HTML)", "tailwind.config = { content: ['<a>&</a>'] }", `{"content":["\u003ca\u003e\u0026\u003c/a\u003e"]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseTailwindConfig(c.in)
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if got != c.want {
				t.Errorf("\n reçu    %s\n attendu %s", got, c.want)
			}
		})
	}
}

func TestParseTailwindConfigRejects(t *testing.T) {
	// Tout ce qui demanderait une évaluation — ou change le sens de la
	// config après coup — est refusé, quelle que soit la façon de l'écrire.
	cases := map[string]string{
		"appel de fonction":                 "tailwind.config = { plugins: [require('x')] }",
		"appel dans une valeur":             "tailwind.config = { theme: { colors: pick('red') } }",
		"expression immédiatement invoquée": "tailwind.config = (function(){ return {} })()",
		"fonction fléchée":                  "tailwind.config = { theme: { extend: () => ({}) } }",
		"accesseur":                         "tailwind.config = { get theme() { return {} } }",
		"méthode":                           "tailwind.config = { theme() { return {} } }",
		"gabarit avec interpolation":        "tailwind.config = { theme: { colors: { a: `${x}` } } }",
		"gabarit simple":                    "tailwind.config = { theme: { colors: { a: `red` } } }",
		"spread":                            "tailwind.config = { ...base }",
		"clé calculée":                      "tailwind.config = { [key]: 1 }",
		"identifiant comme valeur":          "tailwind.config = { theme: { colors: colors } }",
		"undefined":                         "tailwind.config = { a: undefined }",
		"accès à un objet global":           "tailwind.config = { a: process.env }",
		"seconde instruction":               "tailwind.config = { a: 1 }; tailwind.config.a = 2",
		"instruction avant":                 "var x = 1; tailwind.config = { a: 1 }",
		"affectation à autre chose":         "tailwind.confiG = { a: 1 }",
		"comparaison, pas affectation":      "tailwind.config == { a: 1 }",
		"pas un objet":                      "tailwind.config = [1, 2]",
		"chaîne non refermée":               "tailwind.config = { a: 'oups }",
		"commentaire non refermé":           "tailwind.config = { a: 1 /* }",
		"opérateur":                         "tailwind.config = { a: 1 + 1 }",
		"virgule sans valeur":               "tailwind.config = { a: , }",
		"objet non refermé":                 "tailwind.config = { a: { b: 1 }",
		"échappement de ligne de code":      "tailwind.config = { a: '\\z' }",
		"trop profond":                      "tailwind.config = " + strings.Repeat("{a:", 40) + "1" + strings.Repeat("}", 40),
		"trop gros":                         "tailwind.config = { a: '" + strings.Repeat("x", maxConfigBytes) + "' }",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseTailwindConfig(in)
			if err == nil {
				t.Fatalf("accepté à tort : %s", got)
			}
			if !errors.Is(err, errNotLiteral) {
				t.Errorf("erreur non typée : %v", err)
			}
		})
	}
}

// fakeTailwind : un « binaire » qui note sous quelle identité et dans quel
// monde il tourne, à la place du CSS. Placé dans un dossier lisible par tous :
// sous root, le vrai binaire est lancé sous un compte sans droits.
func fakeTailwind(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "np-fake-tw-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Chmod(dir, 0o755)
	bin := filepath.Join(dir, "tailwindcss")
	script := "#!/bin/sh\nset -e\n" +
		"[ \"$1\" = -c ] && [ \"$2\" = config.cjs ] || { echo mauvais args >&2; exit 2; }\n" +
		"printf 'uid=%s\\ngroups=%s\\nhome=%s\\ncwd=%s\\nfiles=%s\\nenv=%s\\n' \"$(id -u)\" \"$(id -G)\" \"$HOME\" \"$PWD\" \"$(ls | tr '\\n' ' ')\" \"$(env | cut -d= -f1 | sort | tr '\\n' ' ')\" > out.css\n" +
		"cat config.cjs >> out.css\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestCompileTailwindConfinement(t *testing.T) {
	page := []byte(`<script>tailwind.config = { theme: { extend: { colors: { brand: '#123456' } } } }</script><p class="text-brand">x</p>`)
	body, _, _ := extractTailwindConfig([]byte(`<script>tailwind.config = { theme: { extend: { colors: { brand: '#123456' } } } }</script>`))

	out, err := compileTailwind(fakeTailwind(t), page, body)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	lines := map[string]string{}
	for _, l := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(l, "module.exports") {
			lines[k] = v
		}
	}
	// Le fichier de config ne contient qu'une donnée : le JSON du littéral.
	if !strings.Contains(s, `module.exports = {"theme":{"extend":{"colors":{"brand":"#123456"}}}};`) {
		t.Errorf("config attendue en JSON :\n%s", s)
	}
	// Monde clos : répertoire temporaire comme cwd et HOME, page copiée dedans.
	if lines["cwd"] != lines["home"] || !strings.HasPrefix(lines["cwd"], os.TempDir()) {
		t.Errorf("cwd/HOME : %q / %q", lines["cwd"], lines["home"])
	}
	for _, f := range []string{"config.cjs", "input.css", "page.html"} {
		if !strings.Contains(lines["files"], f) {
			t.Errorf("%s absent du répertoire de build : %q", f, lines["files"])
		}
	}
	// Environnement minimal : rien de l'environnement du service ne fuit.
	if env := strings.Fields(lines["env"]); len(env) > 6 || strings.Contains(lines["env"], "GEMINI") {
		t.Errorf("environnement transmis : %q", lines["env"])
	}
	// Identité : sous root, le binaire tourne sous le compte sans droits.
	uid, _ := strconv.Atoi(lines["uid"])
	if os.Geteuid() == 0 {
		if buildUser == nil {
			t.Skip("root sans compte nobody/www-data : abandon de privilèges impossible ici")
		}
		if uid != int(buildUser.Uid) || uid == 0 {
			t.Errorf("uid du binaire = %d, attendu %d (compte sans droits)", uid, buildUser.Uid)
		}
		for _, g := range strings.Fields(lines["groups"]) {
			if g == "0" {
				t.Errorf("le binaire conserve le groupe root : %q", lines["groups"])
			}
		}
	} else if uid != os.Geteuid() {
		t.Errorf("uid du binaire = %d, attendu %d", uid, os.Geteuid())
	}
}

// TestCompileTailwindNeverExecutesPageScript : la preuve de non-exécution.
// Une config qui n'est pas un pur littéral est refusée AVANT tout lancement
// du binaire ; un fichier témoin que ce code tenterait de créer n'apparaît pas.
func TestCompileTailwindNeverExecutesPageScript(t *testing.T) {
	witness := filepath.Join(t.TempDir(), "temoin.txt")
	bin := fakeTailwind(t)
	launched := filepath.Join(filepath.Dir(bin), "launched")
	os.WriteFile(bin, []byte("#!/bin/sh\ntouch "+launched+"\n"), 0o755)

	page := []byte("<p>x</p>")
	witnessQ := strconv.Quote(witness)
	for name, cfg := range map[string]string{
		"appel de fonction dans une valeur": "tailwind.config = { theme: { x: require('fs').writeFileSync(" + witnessQ + ", '') } }",
		"expression immédiatement invoquée": "tailwind.config = (() => { require('fs').writeFileSync(" + witnessQ + ", ''); return {} })()",
		"accesseur":                         "tailwind.config = { get theme() { require('fs').writeFileSync(" + witnessQ + ", ''); return {} } }",
		"gabarit":                           "tailwind.config = { a: `${require('fs').writeFileSync(" + witnessQ + ", '')}` }",
		"seconde instruction":               "tailwind.config = {}; require('fs').writeFileSync(" + witnessQ + ", '')",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compileTailwind(bin, page, cfg)
			if !errors.Is(err, errNotLiteral) {
				t.Fatalf("attendu un refus de config, reçu %v", err)
			}
			if _, err := os.Stat(witness); err == nil {
				t.Fatal("le fichier témoin existe : du code de la page a été exécuté")
			}
			if _, err := os.Stat(launched); err == nil {
				t.Fatal("le binaire a été lancé malgré une config refusée")
			}
		})
	}
}

// Avec le vrai binaire (s'il est là) : une config légitime compile bien et
// ses jetons se retrouvent dans le CSS — sous le compte sans droits si root.
func TestCompileTailwindRealBinary(t *testing.T) {
	bin := "../../bin/tailwindcss"
	if _, err := os.Stat(bin); err != nil {
		t.Skip("binaire Tailwind absent")
	}
	abs, _ := filepath.Abs(bin)
	page := []byte(`<p class="text-brand font-titre">x</p>`)
	css, err := compileTailwind(abs, page, "tailwind.config = { theme: { extend: { colors: { brand: '#123456' }, fontFamily: { titre: ['Syne', 'sans-serif'] } } } }")
	if err != nil {
		t.Fatal(err)
	}
	// Tailwind réécrit les hex en rgb() et déquote les familles.
	if !strings.Contains(string(css), "18 52 86") || !strings.Contains(string(css), "Syne") {
		t.Errorf("jetons absents du CSS compilé :\n%s", css)
	}
}

// La sortie du build est lue sans jamais suivre un lien : un binaire (ou un
// processus du compte de build) qui remplacerait out.css par un lien vers un
// fichier de root ne fait pas lire ce fichier.
func TestCompileTailwindOutputMustBeRegularFile(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(secret, []byte("ne-pas-lire"), 0o600)
	bin := fakeTailwind(t)
	os.WriteFile(bin, []byte("#!/bin/sh\nln -s "+secret+" out.css\n"), 0o755)
	page := []byte("<p>x</p>")

	out, err := compileTailwind(bin, page, "")
	if err == nil || strings.Contains(string(out), "ne-pas-lire") {
		t.Fatalf("un lien à la place du CSS doit être refusé : err=%v out=%q", err, out)
	}
}

// Le binaire est lancé depuis un répertoire temporaire : le chemin relatif
// par défaut (./bin/tailwindcss) doit avoir été absolutisé par NewStore.
func TestNewStoreAbsolutizesTailwindBin(t *testing.T) {
	if _, err := os.Stat("../../bin/tailwindcss"); err != nil {
		t.Skip("binaire Tailwind absent")
	}
	st, err := NewStore(t.TempDir(), "../../bin/tailwindcss", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(st.twBin) {
		t.Fatalf("twBin non absolutisé : %q", st.twBin)
	}
	css, err := st.CompileCSS([]byte(`<p class="text-brand">x</p>`), "tailwind.config = { theme: { extend: { colors: { brand: '#123456' } } } }")
	if err != nil || !strings.Contains(string(css), "18 52 86") {
		t.Fatalf("compilation avec chemin relatif : err=%v", err)
	}
}
