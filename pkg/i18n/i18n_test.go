package i18n

import (
	"strings"
	"testing"
)

func srcs(segs []Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.Src
		if s.Attr != "" {
			out[i] = "@" + s.Attr + ":" + s.Src
		}
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestExtract(t *testing.T) {
	cases := []struct {
		name, html string
		want       []string
	}{
		{"paragraphes", `<section><h2 class="text-4xl">Bonjour   le
			monde</h2><p>Deuxième phrase.</p></section>`, []string{"Bonjour le monde", "Deuxième phrase."}},
		{"marqueurs en ligne", `<p>Lisez <a href="/cgu" class="underline">nos <strong>conditions</strong></a>.<br>Merci</p>`,
			[]string{"Lisez <1>nos <2>conditions</2></1>.<3/>Merci"}},
		{"icône dans un bouton", `<button class="btn"><svg viewBox="0 0 24 24"><path d="M0 0"/><text>x</text></svg> Commencer</button>`,
			[]string{"Commencer"}},
		{"liens côte à côte", `<nav><a href="#">Accueil</a> <a href="#">Tarifs</a><a href="#"><svg></svg></a></nav>`, []string{"Accueil", "Tarifs"}},
		{"libellé + valeur", `<div><span class="text-xs">Est. Pipeline</span> <span class="font-bold">$0</span></div>`, []string{"Est. Pipeline"}},
		{"pastille vide", `<p><span class="h-2 w-2 rounded-full"></span> Hors ligne <span class="dot"></span>prêt</p>`, []string{"Hors ligne <1/>prêt"}},
		{"séparateur sans lettre", `<p><span>Botanique</span> <span>•</span> <span>5 min</span></p>`, []string{"Botanique", "5 min"}},
		{"blocs imbriqués", `<div>Avant <div>Dedans</div> Après</div>`, []string{"Avant", "Dedans", "Après"}},
		{"ignorés", `<div><script>var t = "Bonjour"</script><code>npm i</code><pre>Texte</pre><p translate="no">Marque</p><p class="x notranslate">Nom</p></div>`,
			[]string{}},
		{"code en ligne opaque", `<p>Lancez <code>npm i</code> puis attendez</p>`, []string{"Lancez <1/> puis attendez"}},
		{"sans lettre", `<p>99 €</p><p>→</p><span>2026</span>`, []string{}},
		{"attributs", `<div><img src="a.png" alt="Une équipe"><a href="#" aria-label="Fermer le menu"><svg></svg></a><input type="submit" value="Envoyer"><input type="text" placeholder="Votre email" value="ne pas traduire"></div>`,
			[]string{"@alt:Une équipe", "@aria-label:Fermer le menu", "@value:Envoyer", "@placeholder:Votre email"}},
		{"doublons", `<p>Oui</p><li>Oui</li><img alt="Oui">`, []string{"Oui"}},
		{"échappement", `<p>R&amp;D &lt;3</p>`, []string{"R&amp;D &lt;3"}},
		{"insécable conservé", "<p>Prix : 10 €</p>", []string{"Prix : 10 €"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { eq(t, srcs(Extract(c.html)), c.want) })
	}
}

func TestKeyNormalizesSpace(t *testing.T) {
	if Key("Bonjour  le\n monde") != Key("Bonjour le monde") || Key("a") == Key("b") || len(Key("a")) != 16 {
		t.Fatal("Key")
	}
}

func TestValid(t *testing.T) {
	src := "Lisez <1>nos <2>conditions</2></1>.<3/>Merci"
	for dst, want := range map[string]bool{
		"Read <1>our <2>terms</2></1>.<3/>Thanks": true,
		"<3/>Read <1><2>terms</2> of ours</1>":    true,  // réordonné
		"Read <1>our terms</1>.<3/>":              false, // marqueur perdu
		"Read <1>our <2>terms</1></2>.<3/>":       false, // mal imbriqué
		"Read <1>our <2>terms</2></1>.<3/><4/>":   false, // marqueur inventé
		"Read <1>our <2>terms</2></1>.<3/><3/>":   false, // doublé
		"  ":                                      false,
	} {
		if Valid(src, dst) != want {
			t.Errorf("Valid(%q) = %v", dst, !want)
		}
	}
	if !Valid("Bonjour", "Hello <b>world</b>") { // balise non numérotée = texte
		t.Error("texte avec chevrons")
	}
}

func TestApply(t *testing.T) {
	dict := map[string]string{
		"Lisez <1>nos <2>conditions</2></1>.<3/>Merci": "Read <1>our <2>terms</2></1>.<3/>Thanks",
		"Commencer":      "Get started",
		"Une équipe":     "A team",
		"Titre":          "Title",
		"Fermer":         "Close",
		"Cassé <1>x</1>": "Broken",                    // marqueurs perdus ⇒ source gardée
		"Piège":          "<script>alert(1)</script>", // texte, jamais du balisage
	}
	in := `<section class="py-8"><h2 class="text-4xl font-bold">Titre</h2>` +
		`<p class="lead">Lisez <a href="/cgu" class="underline">nos <strong>conditions</strong></a>.<br>Merci</p>` +
		`<button class="btn"><svg viewBox="0 0 24 24"><path d="M0 0"></path></svg> Commencer</button>` +
		`<div><img src="a.png" alt="Une équipe"></div><a href="#" title="Fermer">Fermer</a>` +
		`<p>Cassé <em>x</em></p><p>Piège</p><p>Inconnu</p></section>`
	out, missing := Apply(in, func(s string) (string, bool) { d, ok := dict[s]; return d, ok })

	for _, want := range []string{
		`<h2 class="text-4xl font-bold">Title</h2>`,
		`<p class="lead">Read <a class="underline" href="/cgu">our <strong>terms</strong></a>.<br/>Thanks</p>`,
		`<button class="btn"><svg viewBox="0 0 24 24"><path d="M0 0"></path></svg> Get started</button>`,
		`alt="A team"`,
		`<a href="#" title="Close">Close</a>`,
		`<p>Cassé <em>x</em></p>`,
		`<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>`,
		`<p>Inconnu</p>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manque %s\n%s", want, out)
		}
	}
	eq(t, missing, []string{"Cassé <1>x</1>", "Inconnu"})

	// La sortie traduite se ré-extrait avec les mêmes marqueurs.
	again := srcs(Extract(out))
	if again[0] != "Title" || again[1] != "Read <1>our <2>terms</2></1>.<3/>Thanks" {
		t.Fatalf("ré-extraction : %q", again)
	}
}

func TestApplyKeepsEdgeSpaceAndScripts(t *testing.T) {
	in := "<div>\n  Bonjour <b>vous</b>  \n<script>if (a < b) {}</script></div>"
	out, _ := Apply(in, func(s string) (string, bool) { return "Hello <1>you</1>", s == "Bonjour <1>vous</1>" })
	if out != "<div>\n  Hello <b>you</b>  \n<script>if (a < b) {}</script></div>" {
		t.Fatalf("%q", out)
	}
}

func TestLangRe(t *testing.T) {
	for _, l := range []string{"fr", "en", "pt-br", "zh-hant", "yue"} {
		if !LangRe.MatchString(l) {
			t.Error(l)
		}
	}
	for _, l := range []string{"", "FR", "e", "en_US", "../x", "en-"} {
		if LangRe.MatchString(l) {
			t.Error(l)
		}
	}
}
