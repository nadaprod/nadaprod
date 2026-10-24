package sites

import "testing"

func TestInertScripts(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"inline", `<script>document.documentElement.classList.add('js');</script>`,
			`<script type="text/x-np-inert">document.documentElement.classList.add('js');</script>`},
		{"src", `<script src="app.js" defer></script>`,
			`<script type="text/x-np-inert" src="app.js" defer></script>`},
		{"module keeps its type", `<script type="module" src="m.js"></script>`,
			`<script type="text/x-np-inert" data-np-type="module" src="m.js"></script>`},
		{"explicit js type", `<SCRIPT TYPE='text/javascript'>x()</SCRIPT>`,
			`<script type="text/x-np-inert" data-np-type="text/javascript">x()</SCRIPT>`},
		{"data-type is not type", `<script data-type="a">x()</script>`,
			`<script type="text/x-np-inert" data-type="a">x()</script>`},
		{"json-ld untouched", `<script type="application/ld+json">{"a":1}</script>`,
			`<script type="application/ld+json">{"a":1}</script>`},
		{"tailwind cdn kept", `<script src="https://cdn.tailwindcss.com"></script>`,
			`<script src="https://cdn.tailwindcss.com"></script>`},
		{"tailwind config kept", `<script>tailwind.config = {theme:{}}</script>`,
			`<script>tailwind.config = {theme:{}}</script>`},
		{"text around", `<p>a</p><script>b()</script><p>c</p>`,
			`<p>a</p><script type="text/x-np-inert">b()</script><p>c</p>`},
		{"unclosed", `<script>x()`, `<script type="text/x-np-inert">x()`},
		{"no script", `<p>rien</p>`, `<p>rien</p>`},
		{"script string in body", `<script>document.write('<script src="x.js"></scr'+'ipt>')</script><script>y()</script>`,
			`<script type="text/x-np-inert">document.write('<script src="x.js"></scr'+'ipt>')</script><script type="text/x-np-inert">y()</script>`},
	}
	for _, c := range cases {
		if got := string(inertScripts([]byte(c.in))); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}
}

// Aller-retour : ce que l'éditeur affiche puis renvoie redevient la source.
func TestRestoreScripts(t *testing.T) {
	for _, src := range []string{
		`<script>document.documentElement.classList.add('js');</script>`,
		`<script src="app.js" defer></script>`,
		`<script type="module" src="m.js"></script>`,
		`<script type="application/ld+json">{"a":1}</script>`,
		`<script src="https://cdn.tailwindcss.com"></script><p>x</p><script>y()</script>`,
	} {
		if got := string(restoreScripts(inertScripts([]byte(src)))); got != src {
			t.Errorf("aller-retour :\n got  %s\n want %s", got, src)
		}
	}
}
