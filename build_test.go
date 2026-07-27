package main

import (
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
