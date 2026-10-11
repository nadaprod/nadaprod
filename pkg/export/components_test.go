package export

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// nbsp est construit par code : un U+00A0 littéral dans le source ne survivrait pas
// à tous les éditeurs (c'est précisément ce que le convertisseur évite en sortie).
var nbsp = string(rune(0xa0))

func mustJSX(t *testing.T, fragment string) string {
	t.Helper()
	out, err := ToJSX(fragment, "Hero")
	if err != nil {
		t.Fatalf("ToJSX : %v", err)
	}
	return out
}

func mustVue(t *testing.T, fragment string) string {
	t.Helper()
	out, err := ToVue(fragment, "Hero")
	if err != nil {
		t.Fatalf("ToVue : %v", err)
	}
	return out
}

func expectParts(t *testing.T, got string, want, not []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("attendu %q dans :\n%s", w, got)
		}
	}
	for _, n := range not {
		if strings.Contains(got, n) {
			t.Errorf("inattendu %q dans :\n%s", n, got)
		}
	}
}

const svgFixture = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" class="h-6 w-6" fill="none" viewbox="0 0 24 24" preserveaspectratio="xMidYMid meet" stroke="currentColor" stroke-width="2" aria-hidden="true">
  <defs>
    <lineargradient id="g" gradientunits="userSpaceOnUse" gradienttransform="rotate(45)"><stop offset="0" stop-color="#fff" stop-opacity=".5"/></lineargradient>
    <clippath id="c"><rect width="10" height="10"/></clippath>
  </defs>
  <path stroke-linecap="round" stroke-linejoin="round" fill-rule="evenodd" clip-rule="evenodd" d="M5 13l4 4L19 7"/>
  <use xlink:href="#g" clip-path="url(#c)"/>
  <text x="0" y="10" text-anchor="middle" font-size="12" dominant-baseline="middle">Salut</text>
</svg>`

func TestToJSX(t *testing.T) {
	cases := []struct {
		name, in  string
		want, not []string
	}{
		{
			name: "class et for",
			in:   `<div class="p-4"><label for="email">Email</label><input id="email" type="email" required></div>`,
			want: []string{`<div className="p-4">`, `<label htmlFor="email">Email</label>`, `<input id="email" type="email" required />`},
			not:  []string{` class=`, ` for=`},
		},
		{
			name: "attributs HTML en camelCase",
			in: `<form novalidate enctype="multipart/form-data" accept-charset="utf-8"><input tabindex="1" readonly maxlength="10" autocomplete="off">` +
				`<img src="a.png" srcset="a.png 2x" crossorigin="anonymous" alt=""><time datetime="2026-01-01">1er janvier</time>` +
				`<table><tr><td colspan="2" rowspan="3">x</td></tr></table></form>`,
			want: []string{`<form noValidate encType="multipart/form-data" acceptCharset="utf-8">`, `tabIndex="1" readOnly maxLength="10" autoComplete="off"`,
				`srcSet="a.png 2x" crossOrigin="anonymous"`, `<time dateTime="2026-01-01">`, `<td colSpan="2" rowSpan="3">x</td>`},
		},
		{
			name: "aria-* et data-* inchangés",
			in:   `<button aria-expanded="false" data-menu-target="nav" type="button">Menu</button>`,
			want: []string{`<button aria-expanded="false" data-menu-target="nav" type="button">Menu</button>`},
		},
		{
			// viewbox/lineargradient… en minuscules dans le source : la casse vient du parseur.
			name: "SVG",
			in:   svgFixture,
			want: []string{`viewBox="0 0 24 24"`, `preserveAspectRatio="xMidYMid meet"`, `strokeWidth="2"`, `xmlnsXlink="http://www.w3.org/1999/xlink"`,
				`className="h-6 w-6"`, `aria-hidden="true"`,
				`<linearGradient id="g" gradientUnits="userSpaceOnUse" gradientTransform="rotate(45)">`, `<stop offset="0" stopColor="#fff" stopOpacity=".5" />`,
				`<clipPath id="c">`, `</clipPath>`, `<rect width="10" height="10" />`,
				`<path strokeLinecap="round" strokeLinejoin="round" fillRule="evenodd" clipRule="evenodd" d="M5 13l4 4L19 7" />`,
				`<use xlinkHref="#g" clipPath="url(#c)" />`,
				`<text x="0" y="10" textAnchor="middle" fontSize="12" dominantBaseline="middle">Salut</text>`},
			not: []string{"stroke-width", "viewbox", "xlink:href", "stop-color", "></path>"},
		},
		{
			name: "style en objet",
			in:   `<div style="background-color: #fff; --brand-color: #4f46e5; -webkit-line-clamp: 3; background-image: url('data:image/png;base64,AA=='); margin:0">x</div>`,
			want: []string{`style={{ backgroundColor: "#fff", "--brand-color": "#4f46e5", WebkitLineClamp: "3", backgroundImage: "url('data:image/png;base64,AA==')", margin: "0" }}`},
		},
		{
			name: "style : !important, doublons, -ms-, déclarations invalides",
			in:   `<div style="color: red !important; display: -webkit-box; display: flex; -ms-transform: none;; : x; 1bad: y; font-family: &quot;Inter&quot;, sans-serif">x</div>`,
			want: []string{`style={{ color: "red", display: "flex", msTransform: "none", fontFamily: "\"Inter\", sans-serif" }}`},
		},
		{
			name: "style vide retiré",
			in:   `<div style=" ; " id="a">x</div>`,
			want: []string{`<div id="a">x</div>`},
			not:  []string{"style="},
		},
		{
			name: "attributs booléens",
			in: `<details open><summary>Question</summary><p>Réponse</p></details>` +
				`<video autoplay="autoplay" muted loop playsinline controls src="v.mp4"></video><div hidden="until-found">x</div>`,
			want: []string{`<details open>`, `<video autoPlay muted loop playsInline controls src="v.mp4" />`, `<div hidden="until-found">`},
			not:  []string{`open=`, `muted=`},
		},
		{
			name: "champs de formulaire non contrôlés",
			in: `<form><input type="text" name="nom" value="Jean" readonly><input type="checkbox" value="oui" checked disabled>` +
				`<select name="plan"><option value="a">A</option><option value="b" selected>B</option></select>` +
				`<select multiple><option selected>Un</option><option value="2" selected>Deux</option><option>Trois</option></select>` +
				"<textarea name=\"msg\">Bonjour,\n  monde</textarea></form>",
			want: []string{`<input type="text" name="nom" defaultValue="Jean" readOnly />`, `<input type="checkbox" value="oui" defaultChecked disabled />`,
				`<select name="plan" defaultValue="b">`, `<option value="b">B</option>`, `<select multiple defaultValue={["Un", "2"]}>`,
				`<textarea name="msg" defaultValue={"Bonjour,\n  monde"} />`},
			not: []string{"selected", " checked", "</textarea>"},
		},
		{
			name: "éléments vides et auto-fermants",
			in:   `<div><img src="a.png" alt=""><hr><input type="email"><div class="h-px bg-slate-200"></div><span> </span></div>`,
			want: []string{`<img src="a.png" alt="" />`, `<hr />`, `<input type="email" />`, `<div className="h-px bg-slate-200" />`, `<span />`},
			not:  []string{"</img>", "</span>", "<hr>"},
		},
		{
			name: "accolades et chevrons dans le texte",
			in:   `<p>Utilisez {variable} si a &lt; b &gt; c &amp; R&amp;D, et &amp;lt; littéral</p>`,
			want: []string{`Utilisez &#123;variable&#125; si a &lt; b &gt; c &amp; R&amp;D, et &amp;lt; littéral`},
			not:  []string{"{variable}"},
		},
		{
			name: "commentaires",
			in:   `<div><!-- Section héro */ fin --><p>x</p></div>`,
			want: []string{`{/* Section héro * / fin */}`, `<p>x</p>`},
			not:  []string{"<!--"},
		},
		{
			name: "texte français conservé en UTF-8",
			in:   `<p>« Créez », l’élégance… dès 19` + nbsp + `€ — cœur à l'œuvre&nbsp;!</p>`,
			want: []string{`« Créez », l’élégance… dès 19&nbsp;€ — cœur à l'œuvre&nbsp;!`},
			not:  []string{nbsp},
		},
		{
			name: "valeurs d'attribut délicates",
			in: `<img src="https://placehold.co/600x400?text=A&amp;font=inter" alt='Logo "NADA"' title="{x}" data-path="a\b" data-nbsp="10` + nbsp + `€"` +
				` aria-label="Tom &amp;amp; Jerry" data-lines="l1&#10;l2" class="[&>li]:mt-2">`,
			want: []string{`src="https://placehold.co/600x400?text=A&font=inter"`, `alt={"Logo \"NADA\""}`, `title={"{x}"}`, `data-path={"a\\b"}`,
				`data-nbsp={"10` + `\` + `u00a0€"}`, `aria-label={"Tom &amp; Jerry"}`, `data-lines={"l1\nl2"}`, `className="[&>li]:mt-2"`},
			not: []string{nbsp},
		},
		{
			name: "texte en ligne sur une seule ligne",
			in:   "<p class=\"lead\">\n  Rejoignez <strong>2 000</strong> clients,\n  <a href=\"#\">ici</a>.\n</p>",
			want: []string{`    <p className="lead">Rejoignez <strong>2 000</strong> clients, <a href="#">ici</a>.</p>`},
			not:  []string{`{" "}`},
		},
		{
			// Le blanc de bord d'un élément inline est remonté chez le parent.
			name: "blancs de bord des éléments inline",
			in:   `<p>foo<b> bar</b> baz<i>qux </i>quux</p>`,
			want: []string{`<p>foo <b>bar</b> baz<i>qux</i> quux</p>`},
		},
		{
			name: "espace significative en fin de ligne",
			in: `<p><a href="#un" class="text-sm font-semibold leading-6 text-slate-900 hover:text-indigo-600">Premier lien</a> ` +
				`<a href="#deux" class="text-sm font-semibold leading-6 text-slate-900 hover:text-indigo-600">Second lien</a></p>`,
			want: []string{"        Premier lien\n      </a>{\" \"}\n      <a href=\"#deux\""},
		},
		{
			name: "aucune espace à préserver dans un conteneur flex",
			in: `<nav class="hidden md:flex gap-8"><a href="#un" class="text-sm font-semibold leading-6 text-slate-900 hover:text-indigo-600">Premier lien</a> ` +
				`<a href="#deux" class="text-sm font-semibold leading-6 text-slate-900 hover:text-indigo-600">Second lien</a> texte</nav>`,
			want: []string{"        Premier lien\n      </a>\n      <a href=\"#deux\"", "        Second lien\n      </a>\n      texte\n    </nav>"},
			not:  []string{`{" "}`},
		},
		{
			name: "span masqué en responsive : l'espace reste",
			in:   `<p>Voir <span class="hidden sm:inline">tous les</span> projets</p>`,
			want: []string{`<p>Voir <span className="hidden sm:inline">tous les</span> projets</p>`},
		},
		{
			name: "ponctuation collée et élément en forme bloc",
			in:   `<p>Essayez <a href="#essai-gratuit-sans-carte-bancaire" class="font-semibold text-indigo-600 underline decoration-2 hover:text-indigo-500">gratuitement</a>, sans carte.</p>`,
			want: []string{"      Essayez{\" \"}\n      <a href=", "        gratuitement\n      </a>, sans carte.\n    </p>"},
		},
		{
			name: "br : un saut de ligne par <br>",
			in:   `<h1 class="t">Créez vite <br> publiez mieux</h1>`,
			want: []string{"    <h1 className=\"t\">\n      Créez vite<br />\n      publiez mieux\n    </h1>"},
		},
		{
			name: "pre : blancs conservés via littéraux",
			in:   "<pre class=\"text-xs\">a\n  b {x} <b>c</b></pre>",
			want: []string{`<pre className="text-xs">{"a\n  b {x} "}<b>{"c"}</b></pre>`},
		},
		{
			name: "ordre des attributs du source (le parseur trie ceux de <a>, <b>…)",
			in:   `<div><a href="#x" class="c" id="i">x</a> <strong title="t" class="k">y</strong></div>`,
			want: []string{`<a href="#x" className="c" id="i">x</a> <strong title="t" className="k">y</strong>`},
		},
		{
			name: "lien-carte enveloppant des blocs",
			in:   `<div><a href="#a"><h3>A</h3></a> <a href="#b"><h3>B</h3></a></div>`,
			want: []string{"</a>\n      <a href=\"#b\">"},
			not:  []string{`{" "}`},
		},
		{
			name: "noms de balise ou d'attribut inexprimables",
			in:   `<div :class="x" v-if="y" x-data="{open:false}"><foo@bar>z</foo@bar></div>`,
			want: []string{`<div v-if="y" x-data={"{open:false}"}>`, `<div>z</div>`},
			not:  []string{":class", "foo@bar"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectParts(t, mustJSX(t, tc.in), tc.want, tc.not)
		})
	}
}

func TestToJSXShape(t *testing.T) {
	got := mustJSX(t, `<section class="py-8"><h1>Titre</h1></section>`)
	want := `// Généré par NADAPROD — composant React (classes Tailwind : voir styles.css).

export default function Hero() {
  return (
    <section className="py-8">
      <h1>Titre</h1>
    </section>
  );
}
`
	if got != want {
		t.Errorf("forme du fichier :\n%s\nattendu :\n%s", got, want)
	}
}

func TestToJSXRemovals(t *testing.T) {
	const first = "// Généré par NADAPROD — composant React (classes Tailwind : voir styles.css).\n"
	cases := []struct {
		name, in, note string
		want, not      []string
	}{
		{
			name: "script et gestionnaires",
			in: `<section><button onclick="toggle()" onmouseover="hint()" class="btn">Menu</button></section>
<script>function toggle() { document.body.classList.toggle('open') }</script>`,
			note: "// NADAPROD — 1 <script> et 2 gestionnaires inline retirés : à réimplémenter avec useState/useEffect.\n",
			want: []string{`<button className="btn">Menu</button>`},
			not:  []string{"</script", "toggle(", "onclick", "onmouseover", "<>"},
		},
		{
			name: "un seul gestionnaire : accord au singulier",
			in:   `<a href="#" onclick="go()">Aller</a>`,
			note: "// NADAPROD — 1 gestionnaire inline retiré : à réimplémenter avec useState/useEffect.\n",
			not:  []string{"onclick", "go()"},
		},
		{
			name: "style",
			in:   `<div><style>.x{color:red}</style><p class="x">Texte</p></div>`,
			note: "// NADAPROD — 1 <style> retiré : CSS à reporter dans styles.css.\n",
			not:  []string{"</style", "color:red"},
		},
		{
			name: "URL javascript: et raccourci @click",
			in:   `<div><a href="javascript:void(0)" @click="open = true">Ouvrir</a></div>`,
			note: "// NADAPROD — 2 gestionnaires inline retirés : à réimplémenter avec useState/useEffect.\n",
			want: []string{`<a href="#">Ouvrir</a>`},
			not:  []string{"javascript:", "@click"},
		},
		{
			name: "rien à signaler",
			in:   `<p>Texte</p>`,
			not:  []string{"retiré"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustJSX(t, tc.in)
			if !strings.HasPrefix(got, first+tc.note+"\nexport default function Hero() {\n") {
				t.Errorf("en-tête inattendu :\n%s", got)
			}
			expectParts(t, got, tc.want, tc.not)
		})
	}
}

func TestToJSXRoots(t *testing.T) {
	t.Run("plusieurs racines : fragment", func(t *testing.T) {
		got := mustJSX(t, "<header class=\"a\">A</header>\n<footer>B</footer>\n<script>x()</script>")
		expectParts(t, got, []string{"  return (\n    <>\n      <header className=\"a\">A</header>\n      <footer>B</footer>\n    </>\n  );\n}\n"}, nil)
	})
	t.Run("texte nu : fragment", func(t *testing.T) {
		expectParts(t, mustJSX(t, `Bonjour <b>monde</b>`), []string{"    <>\n      Bonjour <b>monde</b>\n    </>"}, nil)
	})
	t.Run("racine unique : pas de fragment", func(t *testing.T) {
		expectParts(t, mustJSX(t, "\n  <section>A</section>\n"), []string{"  return (\n    <section>A</section>\n  );"}, []string{"<>"})
	})
	t.Run("racine unique précédée d'un commentaire : commentaire JS, pas de fragment", func(t *testing.T) {
		got := mustJSX(t, "<!-- Bloc héro -->\n<section>A</section>")
		expectParts(t, got, []string{"  return (\n    /* Bloc héro */\n    <section>A</section>\n  );"}, []string{"<>", "{/*"})
	})
}

func TestComponentErrors(t *testing.T) {
	for _, in := range []string{"", "  \n\t ", "<script>alert(1)</script>", "<!-- rien -->", "<style>a{}</style>\n"} {
		if out, err := ToJSX(in, "Hero"); err == nil {
			t.Errorf("ToJSX(%q) : erreur attendue, obtenu :\n%s", in, out)
		}
		if out, err := ToVue(in, "Hero"); err == nil {
			t.Errorf("ToVue(%q) : erreur attendue, obtenu :\n%s", in, out)
		}
	}
	// Le nom est injecté dans le code généré : tout ce qui n'est pas PascalCase ASCII est refusé.
	for _, name := range []string{"", "hero", "1Hero", "Hero()", "Hero Banner", "Héro", "Hero-2"} {
		if _, err := ToJSX("<p>x</p>", name); err == nil {
			t.Errorf("ToJSX : nom %q accepté", name)
		}
		if _, err := ToVue("<p>x</p>", name); err == nil {
			t.Errorf("ToVue : nom %q accepté", name)
		}
	}
	if _, err := ToJSX("<p>x</p>", "Pricing2"); err != nil {
		t.Errorf("nom valide refusé : %v", err)
	}
}

func TestWordWrap(t *testing.T) {
	text := strings.TrimSpace(strings.Repeat("Un texte assez long pour déborder de la largeur visée, avec 19"+nbsp+"€ insécables. ", 6))
	for name, got := range map[string]string{"jsx": mustJSX(t, "<p>"+text+"</p>"), "vue": mustVue(t, "<p>"+text+"</p>")} {
		start := strings.Index(got, "<p>\n")
		end := strings.Index(got, "</p>")
		if start < 0 || end < 0 {
			t.Fatalf("%s : forme bloc attendue :\n%s", name, got)
		}
		lines := strings.Split(strings.TrimSpace(got[start+len("<p>\n"):end]), "\n")
		if len(lines) < 4 {
			t.Errorf("%s : texte non replié (%d lignes)", name, len(lines))
		}
		var words []string
		for _, l := range lines {
			if n := utf8.RuneCountInString(l); n > compMaxWidth {
				t.Errorf("%s : ligne de %d caractères : %q", name, n, l)
			}
			if l != strings.TrimRight(l, " ") {
				t.Errorf("%s : espace en fin de ligne : %q", name, l)
			}
			words = append(words, strings.Fields(l)...)
		}
		// Jamais de coupure sur l'insécable ; le texte se recompose à l'identique.
		if back := strings.Join(words, " "); back != strings.ReplaceAll(text, nbsp, "&nbsp;") {
			t.Errorf("%s : texte altéré :\n%s", name, back)
		}
	}
}

func TestComponentName(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hero", 1, "Hero"},
		{"hero", 0, "Hero"},
		{"hero", 2, "Hero2"},
		{"pricing", 12, "Pricing12"},
		{"call-to-action", 1, "CallToAction"},
		{"call_to action", 3, "CallToAction3"},
		{"FAQ", 1, "FAQ"},
		{"h1-title", 1, "H1Title"},
		{"témoignages", 1, "TMoignages"},
		{"", 1, "Block"},
		{"", 2, "Block2"},
		{"---", 1, "Block"},
		{"3d-hero", 1, "Block"},
		{"<script>", 1, "Script"},
	}
	for _, tc := range cases {
		got := ComponentName(tc.in, tc.n)
		if got != tc.want {
			t.Errorf("ComponentName(%q, %d) = %q, attendu %q", tc.in, tc.n, got, tc.want)
		}
		if !validComponentName(got) {
			t.Errorf("ComponentName(%q, %d) = %q : nom refusé par ToJSX/ToVue", tc.in, tc.n, got)
		}
	}
}

func TestToVue(t *testing.T) {
	cases := []struct {
		name, in  string
		want, not []string
	}{
		{
			name: "attributs HTML conservés",
			in:   `<div class="p-4"><label for="email">Email</label><input id="email" type="email" required maxlength="40" tabindex="1"></div>`,
			want: []string{`<div class="p-4">`, `<label for="email">Email</label>`, `<input id="email" type="email" required maxlength="40" tabindex="1">`},
			not:  []string{"className", "htmlFor", "/>"},
		},
		{
			name: "SVG : kebab-case conservé, casse du parseur",
			in:   svgFixture,
			want: []string{`viewBox="0 0 24 24"`, `stroke-width="2"`, `xmlns:xlink="http://www.w3.org/1999/xlink"`, `<linearGradient id="g" gradientUnits="userSpaceOnUse"`,
				`<stop offset="0" stop-color="#fff" stop-opacity=".5" />`, `<use xlink:href="#g" clip-path="url(#c)" />`, `stroke-linecap="round"`},
			not: []string{"strokeWidth", "xlinkHref", "></path>"},
		},
		{
			name: "éléments vides",
			in:   `<div><img src="a.png" alt=""><br><hr><div class="h-px"></div><details open><summary>Q</summary></details></div>`,
			want: []string{`<img src="a.png" alt="">`, "<br>\n", "<hr>\n", `<div class="h-px"></div>`, `<details open>`},
			not:  []string{"/>", "</img>", "</br>"},
		},
		{
			name: "script et gestionnaires retirés",
			in:   "<section><button onclick=\"toggle()\" class=\"btn\">Menu</button></section>\n<script>toggle = () => {}</script>",
			want: []string{`<button class="btn">Menu</button>`,
				"<script setup>\n// Composant Hero.\n// NADAPROD — 1 balise script et 1 gestionnaire inline retirés : à réimplémenter avec ref()/onMounted().\n</script>\n"},
			not: []string{"onclick", "toggle", "<script>"},
		},
		{
			name: "bloc statique",
			in:   `<p>Texte</p>`,
			want: []string{"<script setup>\n// Composant Hero.\n// Statique pour l'instant"},
			not:  []string{"retiré"},
		},
		{
			name: "moustaches et entités dans le texte",
			in:   `<p>Prix : {{ prix }} {seul} a &lt; b &gt; c &amp; d` + nbsp + `!</p>`,
			want: []string{`Prix : {{ '{{' }} prix }} {seul} a &lt; b &gt; c &amp; d&nbsp;!`},
			not:  []string{"{{ prix", nbsp},
		},
		{
			name: "valeurs d'attribut",
			in:   `<img src="x.png?a=1&amp;b=2" alt='Logo "NADA"' class="[&>li]:mt-2" title="{{ brut }}">`,
			want: []string{`src="x.png?a=1&amp;b=2"`, `alt="Logo &quot;NADA&quot;"`, `class="[&>li]:mt-2"`, `title="{{ brut }}"`},
		},
		{
			name: "directives étrangères retirées",
			in:   `<div :class="x" v-if="y" @click="z()" #slot x-data="{}" id="a">x</div>`,
			want: []string{`<div x-data="{}" id="a">x</div>`, "1 gestionnaire inline retiré"},
			not:  []string{":class", "v-if", "@click", "#slot"},
		},
		{
			name: "plusieurs racines sans enrobage",
			in:   "<header>A</header>\n<footer>B</footer>",
			want: []string{"<template>\n  <header>A</header>\n  <footer>B</footer>\n</template>"},
		},
		{
			// Vue efface le blanc entre deux éléments séparés par un saut de ligne.
			name: "espace entre deux éléments",
			in: `<p><a href="#un" class="text-sm font-semibold leading-6 text-slate-900 hover:text-indigo-600">Premier lien</a> ` +
				`<a href="#deux" class="text-sm font-semibold leading-6 text-slate-900 hover:text-indigo-600">Second lien</a> et du texte</p>`,
			want: []string{"Premier lien</a>{{ ' ' }}\n    <a href=\"#deux\"", "Second lien</a>\n    et du texte\n  </p>"},
		},
		{
			// Vue garde une espace au bord des textes : celui d'un lien reste collé aux balises.
			name: "texte collé aux balises d'un élément inline",
			in:   `<p><a href="#" class="u">Lire <svg viewBox="0 0 1 1"><path d="M0 0"/></svg> la suite</a>, fin</p>`,
			want: []string{"<a href=\"#\" class=\"u\">Lire\n", "      </svg>\n      la suite</a>, fin\n"},
		},
		{
			name: "pre et textarea tels quels",
			in:   "<div><pre>a\n  b &lt;c&gt;</pre><textarea name=\"m\">l1\n  l2</textarea></div>",
			want: []string{"<pre>a\n  b &lt;c&gt;</pre>", "<textarea name=\"m\">l1\n  l2</textarea>"},
		},
		{
			name: "commentaires",
			in:   `<div><!-- avant -- après --><p>x</p></div>`,
			want: []string{`<!-- avant - - après -->`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectParts(t, mustVue(t, tc.in), tc.want, tc.not)
		})
	}
}

func TestToVueShape(t *testing.T) {
	got := mustVue(t, `<section class="py-8"><h1>Titre</h1></section>`)
	want := `<!-- Généré par NADAPROD — composant Vue 3 (classes Tailwind : voir styles.css). -->
<template>
  <section class="py-8">
    <h1>Titre</h1>
  </section>
</template>

<script setup>
// Composant Hero.
// Statique pour l'instant : aucun script ni gestionnaire dans le bloc d'origine.
</script>
`
	if got != want {
		t.Errorf("forme du fichier :\n%s\nattendu :\n%s", got, want)
	}
}

// heroFixture : un bloc réaliste tel que le LLM le produit (icône SVG, image, bouton-lien,
// <details>, script final).
const heroFixture = `<section class="relative overflow-hidden bg-white py-24 sm:py-32">
  <div class="mx-auto max-w-7xl px-6 lg:px-8">
    <span class="inline-flex items-center gap-2 rounded-full bg-indigo-50 px-3 py-1 text-sm font-medium text-indigo-700">
      <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z"/>
      </svg>
      Nouveau : l’éditeur visuel
    </span>
    <h1 class="mt-6 text-4xl font-bold tracking-tight text-slate-900 sm:text-6xl">Créez des pages «&nbsp;élégantes&nbsp;» en quelques minutes</h1>
    <p class="mt-6 text-lg leading-8 text-slate-600">
      Rejoignez <strong>2 000</strong> équipes qui publient plus vite. Essayez <a href="#essai" class="font-semibold text-indigo-600">gratuitement</a>, dès 19&nbsp;€ par mois.
    </p>
    <div class="mt-10 flex items-center gap-x-6">
      <a href="#demarrer" onclick="track('cta')" class="rounded-md bg-indigo-600 px-3.5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-indigo-500">Commencer</a>
      <a href="#demo" class="text-sm font-semibold leading-6 text-slate-900">Voir la démo <span aria-hidden="true">→</span></a>
    </div>
    <img src="https://placehold.co/1200x600?text=Studio" alt="Aperçu du studio" class="mt-16 rounded-xl shadow-xl" width="1200" height="600" loading="lazy">
    <details class="mt-8" open>
      <summary class="cursor-pointer font-medium">Comment ça marche ?</summary>
      <p class="mt-2 text-slate-600">Décrivez votre activité, l'IA génère chaque bloc.</p>
    </details>
  </div>
</section>
<script>
  document.querySelector('details').addEventListener('toggle', () => {});
</script>`

func TestHeroFixture(t *testing.T) {
	t.Run("jsx", func(t *testing.T) {
		got := mustJSX(t, heroFixture)
		expectParts(t, got, []string{
			"// NADAPROD — 1 <script> et 1 gestionnaire inline retirés",
			"export default function Hero() {\n  return (\n    <section className=\"relative overflow-hidden bg-white py-24 sm:py-32\">\n      <div className=",
			`<svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2" aria-hidden="true">`,
			`<path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />`,
			"          </svg>\n          Nouveau : l’éditeur visuel\n        </span>",
			"Créez des pages «&nbsp;élégantes&nbsp;» en quelques minutes",
			`Rejoignez <strong>2 000</strong> équipes qui publient plus vite. Essayez{" "}`,
			"          <a href=\"#essai\" className=\"font-semibold text-indigo-600\">gratuitement</a>, dès 19&nbsp;€\n          par mois.\n",
			`<a href="#demarrer" className="rounded-md bg-indigo-600 px-3.5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-indigo-500">`,
			`Voir la démo <span aria-hidden="true">→</span>`,
			`<img src="https://placehold.co/1200x600?text=Studio" alt="Aperçu du studio" className="mt-16 rounded-xl shadow-xl" width="1200" height="600" loading="lazy" />`,
			`<details className="mt-8" open>`,
			`<summary className="cursor-pointer font-medium">Comment ça marche ?</summary>`,
			"    </section>\n  );\n}\n",
		}, []string{"</script", "onclick", "addEventListener", " class=", "stroke-width", "<>", nbsp})
	})
	t.Run("vue", func(t *testing.T) {
		got := mustVue(t, heroFixture)
		expectParts(t, got, []string{
			"<template>\n  <section class=\"relative overflow-hidden bg-white py-24 sm:py-32\">\n    <div class=",
			`viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">`,
			`<path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />`,
			"        Rejoignez <strong>2 000</strong> équipes qui publient plus vite. Essayez\n        <a href=\"#essai\" class=\"font-semibold text-indigo-600\">gratuitement</a>, dès 19&nbsp;€ par\n        mois.\n",
			`hover:bg-indigo-500">Commencer</a>`,
			`loading="lazy">`,
			`<details class="mt-8" open>`,
			"  </section>\n</template>\n\n<script setup>\n// Composant Hero.\n// NADAPROD — 1 balise script et 1 gestionnaire inline retirés",
		}, []string{"onclick", "addEventListener", "className", "strokeWidth", `{" "}`, nbsp})
	})
	t.Run("déterministe", func(t *testing.T) {
		if mustJSX(t, heroFixture) != mustJSX(t, heroFixture) || mustVue(t, heroFixture) != mustVue(t, heroFixture) {
			t.Error("deux conversions du même fragment diffèrent")
		}
	})
}
