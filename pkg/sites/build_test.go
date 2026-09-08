package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractTailwindConfig(t *testing.T) {
	cases := []struct {
		name       string
		html       string
		wantFound  bool
		wantStrict bool
		wantIn     string // extrait attendu dans le body
	}{
		{
			name:      "sans config",
			html:      `<head><script src="https://cdn.tailwindcss.com"></script></head>`,
			wantFound: false,
		},
		{
			name:       "config stricte (cas pp/radio.html)",
			html:       "<script>\n tailwind.config = { theme: { extend: { colors: { accent: 'var(--accent)' } } } }\n</script>",
			wantFound:  true,
			wantStrict: true,
			wantIn:     "var(--accent)",
		},
		{
			name:       "config mêlée à d'autres instructions",
			html:       `<script>console.log('hi'); tailwind.config = {theme:{}};</script>`,
			wantFound:  true,
			wantStrict: false,
			wantIn:     "console.log",
		},
		{
			name:       "document sur une seule ligne (cas the-se)",
			html:       `<html><head><script src="https://cdn.tailwindcss.com"></script><script>tailwind.config={darkMode:'class'}</script></head><body></body></html>`,
			wantFound:  true,
			wantStrict: true,
			wantIn:     "darkMode",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, strict, found := extractTailwindConfig([]byte(tc.html))
			if found != tc.wantFound {
				t.Fatalf("found = %v, attendu %v", found, tc.wantFound)
			}
			if !found {
				return
			}
			if strict != tc.wantStrict {
				t.Errorf("strict = %v, attendu %v", strict, tc.wantStrict)
			}
			if !strings.Contains(body, tc.wantIn) {
				t.Errorf("body %q ne contient pas %q", body, tc.wantIn)
			}
		})
	}
}

func TestRewriteTailwindHTML(t *testing.T) {
	css := []byte(".p-4{padding:1rem}")

	t.Run("tag CDN remplacé par le style inline", func(t *testing.T) {
		in := `<head><script src="https://cdn.tailwindcss.com"></script></head><body class="p-4"></body>`
		out := string(rewriteTailwindHTML([]byte(in), css))
		if strings.Contains(out, "cdn.tailwindcss.com") {
			t.Error("le tag CDN est toujours présent")
		}
		if !strings.Contains(out, `<style data-np-tailwind>.p-4{padding:1rem}</style>`) {
			t.Errorf("style inline absent : %s", out)
		}
	})

	t.Run("config stricte retirée", func(t *testing.T) {
		in := `<script src="https://cdn.tailwindcss.com"></script><script>tailwind.config = {theme:{}}</script>`
		out := string(rewriteTailwindHTML([]byte(in), css))
		if strings.Contains(out, "tailwind.config") {
			t.Errorf("le script de config strict aurait dû disparaître : %s", out)
		}
	})

	t.Run("config mixte conservée mais neutralisée", func(t *testing.T) {
		in := `<script src="https://cdn.tailwindcss.com"></script><script>console.log(1);tailwind.config={theme:{}}</script>`
		out := string(rewriteTailwindHTML([]byte(in), css))
		if !strings.Contains(out, "console.log(1)") {
			t.Error("le script mixte ne doit pas être supprimé")
		}
		if !strings.Contains(out, "window.tailwind=window.tailwind||{config:{}}") {
			t.Error("neutraliseur window.tailwind absent")
		}
	})

	t.Run("tags CDN surnuméraires supprimés", func(t *testing.T) {
		in := `<script src="https://cdn.tailwindcss.com"></script><script src="https://cdn.tailwindcss.com"></script>`
		out := string(rewriteTailwindHTML([]byte(in), css))
		if got := strings.Count(out, "<style"); got != 1 {
			t.Errorf("attendu 1 style, obtenu %d : %s", got, out)
		}
		if strings.Contains(out, "cdn.tailwindcss.com") {
			t.Error("un tag CDN subsiste")
		}
	})

	t.Run("les autres scripts sont intacts", func(t *testing.T) {
		in := `<script src="https://cdn.tailwindcss.com"></script><script src="/app.js"></script><script>init()</script>`
		out := string(rewriteTailwindHTML([]byte(in), css))
		if !strings.Contains(out, `<script src="/app.js"></script>`) || !strings.Contains(out, "init()") {
			t.Errorf("scripts tiers altérés : %s", out)
		}
	})
}

func TestInsertBefore(t *testing.T) {
	t.Run("avant </body>", func(t *testing.T) {
		out := string(insertBefore([]byte(`<body><p>x</p></body>`), bodyCloseRe, "<i>y</i>"))
		if out != `<body><p>x</p><i>y</i></body>` {
			t.Errorf("insertion incorrecte : %s", out)
		}
	})
	t.Run("sans </body> : ajout en fin", func(t *testing.T) {
		out := string(insertBefore([]byte(`<p>x</p>`), bodyCloseRe, "<i>y</i>"))
		if out != `<p>x</p><i>y</i>` {
			t.Errorf("ajout en fin incorrect : %s", out)
		}
	})
	t.Run("</BODY> insensible à la casse", func(t *testing.T) {
		out := string(insertBefore([]byte(`<BODY></BODY>`), bodyCloseRe, "!"))
		if out != `<BODY>!</BODY>` {
			t.Errorf("casse non gérée : %s", out)
		}
	})
}

func TestGDPRTranslations(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("de.json", `{"gdpr.title":"Null Stress.","gdpr.body":"Keine Cookies.","gdpr.legal":"Impressum"}`)
	write("es.json", `{"autre.cle":"sans bandeau"}`) // pas de clés gdpr → ignorée
	write("notes.txt", "pas un json")

	trs := gdprTranslations(dir)
	if _, ok := trs["fr"]; !ok {
		t.Error("fr (repli embarqué) manquant")
	}
	if _, ok := trs["en"]; !ok {
		t.Error("en (repli embarqué) manquant")
	}
	de, ok := trs["de"]
	if !ok || de.Title != "Null Stress." || de.OK != "OK" || de.Aria != "Privacy" {
		t.Errorf("de mal chargé : %+v", de)
	}
	if _, ok := trs["es"]; ok {
		t.Error("es sans clés gdpr aurait dû être ignorée")
	}

	// Dossier absent → replis uniquement.
	trs = gdprTranslations(filepath.Join(dir, "inexistant"))
	if len(trs) != 2 {
		t.Errorf("replis attendus seuls, obtenu %d langues", len(trs))
	}
}

func TestBuildGDPRTag(t *testing.T) {
	tag := buildGDPRTag(map[string]gdprText{
		"en": {"Privacy", "Zero <stress>", "Body & body.", "Legal", "OK"},
	})
	if !strings.Contains(tag, `id="__np_gdpr"`) || !strings.Contains(tag, "URLSearchParams") {
		t.Fatal("gabarit incomplet")
	}
	// json.Marshal doit avoir échappé < > & : rien ne peut fermer le <script>.
	if strings.Contains(tag, "Zero <stress>") || strings.Contains(tag, "Body & body") {
		t.Error("caractères HTML non échappés dans le dictionnaire embarqué")
	}
	if !strings.Contains(tag, `\u003cstress\u003e`) {
		t.Error("échappement JSON attendu (\\u003c)")
	}
}
