package images

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

const (
	testURL     = "https://img.test/p.jpg?auto=compress&w=1600"
	testURLEsc  = "https://img.test/p.jpg?auto=compress&amp;w=1600"
	testCredit  = `data-photo-credit="Jane Doe — fake"`
	testTailEnd = ` ` + testCredit + ` loading="lazy"`
)

// singlePhoto : toujours la même photo ⇒ le choix est trivial et la sortie
// attendue s'écrit à la main.
func singlePhoto(string, string, int) ([]Photo, error) {
	return []Photo{{ID: "1", Provider: "fake", URL: testURL, Author: "Jane Doe", Alt: "English alt from provider"}}, nil
}

func TestResolveTagForms(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"exemple du contrat",
			`<img src="https://placehold.co/1200x800/1e293b/94a3b8?text=Equipe" data-photo="startup team meeting" alt="L'équipe en réunion" class="rounded-2xl w-full h-80 object-cover">`,
			`<img src="` + testURLEsc + `" data-photo="startup team meeting" alt="L'équipe en réunion" class="rounded-2xl w-full h-80 object-cover"` + testTailEnd + `>`,
		},
		{
			"data-photo avant src, guillemets simples",
			`<img data-photo='office desk' class='w-full' src='https://placehold.co/600x400'>`,
			`<img data-photo='office desk' class='w-full' src='` + testURLEsc + `'` + testTailEnd + `>`,
		},
		{
			"guillemets mélangés, src au milieu",
			`<img alt='Un "vrai" bureau' src="https://placehold.co/600x400" data-photo='office desk'>`,
			`<img alt='Un "vrai" bureau' src="` + testURLEsc + `" data-photo='office desk'` + testTailEnd + `>`,
		},
		{
			"auto-fermante avec espace",
			`<img src="https://placehold.co/600x400" data-photo="office desk" />`,
			`<img src="` + testURLEsc + `" data-photo="office desk"` + testTailEnd + ` />`,
		},
		{
			"auto-fermante sans espace",
			`<img src="https://placehold.co/600x400" data-photo="office desk"/>`,
			`<img src="` + testURLEsc + `" data-photo="office desk"` + testTailEnd + `/>`,
		},
		{
			"majuscules",
			`<IMG SRC="https://placehold.co/600x400" DATA-PHOTO="Office Desk" ALT="Bureau">`,
			`<IMG SRC="` + testURLEsc + `" DATA-PHOTO="Office Desk" ALT="Bureau"` + testTailEnd + `>`,
		},
		{
			"retours à la ligne et tabulations dans la balise",
			"<img\n\tsrc = \"https://placehold.co/600x400\"\r\n\tdata-photo\n=\n\"office desk\"\n\talt=\"Bureau\"\n>",
			"<img\n\tsrc = \"" + testURLEsc + "\"\r\n\tdata-photo\n=\n\"office desk\"\n\talt=\"Bureau\"" + testTailEnd + "\n>",
		},
		{
			"src sans guillemets",
			`<img src=https://placehold.co/600x400 data-photo="office desk">`,
			`<img src="` + testURLEsc + `" data-photo="office desk"` + testTailEnd + `>`,
		},
		{
			"src sans guillemets en dernier",
			`<img data-photo="office desk" src=https://placehold.co/600x400>`,
			`<img data-photo="office desk" src="` + testURLEsc + `"` + testTailEnd + `>`,
		},
		{
			"src vide",
			`<img src="" data-photo="office desk">`,
			`<img src="` + testURLEsc + `" data-photo="office desk"` + testTailEnd + `>`,
		},
		{
			"src absent",
			`<img data-photo="office desk" alt="Bureau">`,
			`<img src="` + testURLEsc + `" data-photo="office desk" alt="Bureau"` + testTailEnd + `>`,
		},
		{
			"loading déjà présent",
			`<img loading="eager" src="https://placehold.co/600x400" data-photo="office desk">`,
			`<img loading="eager" src="` + testURLEsc + `" data-photo="office desk" ` + testCredit + `>`,
		},
		{
			"crédit déjà présent (placeholder remis par le modèle) : mis à jour, pas dupliqué",
			`<img src="https://placehold.co/600x400" data-photo="office desk" data-photo-credit="Ancien — pexels" loading="lazy">`,
			`<img src="` + testURLEsc + `" data-photo="office desk" ` + testCredit + ` loading="lazy">`,
		},
		{
			"chevron dans une valeur d'attribut",
			`<img alt="a > b" src="https://placehold.co/600x400" data-photo="office desk" title='x>y'>`,
			`<img alt="a > b" src="` + testURLEsc + `" data-photo="office desk" title='x>y'` + testTailEnd + `>`,
		},
		{
			"alt vide ou absent : jamais inventé",
			`<img alt="" src="https://placehold.co/600x400" data-photo="office desk">`,
			`<img alt="" src="` + testURLEsc + `" data-photo="office desk"` + testTailEnd + `>`,
		},
		{
			"entités dans data-photo",
			`<img src="https://placehold.co/600x400" data-photo="caf&eacute; &amp; terrace">`,
			`<img src="` + testURLEsc + `" data-photo="caf&eacute; &amp; terrace"` + testTailEnd + `>`,
		},
		{
			"seul le premier src compte, comme pour le navigateur",
			`<img src="https://placehold.co/600x400" src="https://dup.test/x.png" data-photo="office desk">`,
			`<img src="` + testURLEsc + `" src="https://dup.test/x.png" data-photo="office desk"` + testTailEnd + `>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(&fakeProvider{fn: singlePhoto})
			// Enrobage : le contexte autour de la balise doit ressortir intact.
			pre, post := "<div class=\"p-4\">\r\n  ", "\n</div>\n"
			got, n := s.Resolve(context.Background(), pre+tc.in+post)
			if n != 1 {
				t.Fatalf("n = %d, attendu 1", n)
			}
			if got != pre+tc.want+post {
				t.Errorf("\n reçu    %q\n attendu %q", got, pre+tc.want+post)
			}
		})
	}
}

func TestResolveUntouched(t *testing.T) {
	cases := map[string]string{
		"sans data-photo":           `<img src="https://placehold.co/600x400" alt="Logo">`,
		"data-photo vide":           `<img src="https://placehold.co/600x400" data-photo="">`,
		"data-photo blanc":          `<img src="https://placehold.co/600x400" data-photo="  ">`,
		"data-photo sans valeur":    `<img src="https://placehold.co/600x400" data-photo>`,
		"en commentaire":            `<!-- <img src="https://placehold.co/600x400" data-photo="office desk"> -->`,
		"dans un script":            `<script>const t = '<img src="x" data-photo="office desk">';</script>`,
		"dans un style":             `<STYLE>/* <img src="x" data-photo="office desk"> */</STYLE>`,
		"dans une valeur":           `<div data-tpl="<img src='x' data-photo='office desk'>"></div>`,
		"autre balise":              `<imgx src="x" data-photo="office desk"></imgx><picture data-photo="office desk"></picture>`,
		"balise non fermée":         `<p>fin tronquée <img src="https://placehold.co/600x400" data-photo="office desk"`,
		"guillemet non fermé":       `<img src="https://placehold.co/600x400 data-photo=office>`,
		"texte avec des chevrons":   `<p>1 < 2 et 3 > 2, a<b</p>`,
		"fragment vide":             ``,
		"data-photo hors d'une img": `<p>attribut data-photo="x" cité dans le texte</p>`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeProvider{fn: singlePhoto}
			got, n := NewService(f).Resolve(context.Background(), in)
			if got != in || n != 0 {
				t.Errorf("n=%d\n reçu    %q\n attendu %q", n, got, in)
			}
			if f.count() != 0 {
				t.Errorf("%d recherche(s) lancée(s) pour rien", f.count())
			}
		})
	}
}

// Une vraie balise après un commentaire, un script ou une balise cassée est
// tout de même résolue.
func TestResolveAfterSkippedRegions(t *testing.T) {
	img := `<img src="https://placehold.co/600x400" data-photo="office desk">`
	want := `<img src="` + testURLEsc + `" data-photo="office desk"` + testTailEnd + `>`
	for _, pre := range []string{
		`<!-- ` + img + ` -->`,
		`<script>if (a < b) { x = '` + img + `'; }</SCRIPT >`,
		`<textarea>` + img + `</textarea>`,
		`<p>1 < 2</p>`,
		`<p title="jamais fermé>`, // aucun autre « " » avant la fin de la balise suivante… voir plus bas
	} {
		got, n := NewService(&fakeProvider{fn: singlePhoto}).Resolve(context.Background(), pre+"\n"+img)
		if strings.Contains(pre, "jamais fermé") {
			// Comme dans un navigateur, le guillemet ouvert avale la suite
			// jusqu'au prochain « " » : l'image fait alors partie du <p>.
			// L'important est de ne rien casser.
			if n != 0 || got != pre+"\n"+img {
				t.Errorf("guillemet non fermé : n=%d got=%q", n, got)
			}
			continue
		}
		if n != 1 || got != pre+"\n"+want {
			t.Errorf("n=%d\n reçu    %q\n attendu %q", n, got, pre+"\n"+want)
		}
	}
}

var imgTagRe = regexp.MustCompile(`(?is)<img\b[^>]*>`)

// Tout ce qui est hors des balises <img> modifiées ressort octet pour octet.
func TestResolvePreservesEverythingElse(t *testing.T) {
	fragment := "<section class=\"py-24   bg-white\"  >\r\n" +
		"\t<!-- héro : <img data-photo=\"ignored\"> -->\r\n" +
		"  <h2 class='text-4xl'>L'équipe &amp; ses « valeurs » — 100&nbsp;% ✓</h2>\n\n\n" +
		"  <IMG SRC='https://placehold.co/1200x800?text=A&amp;B' DATA-PHOTO='startup team' ALT=\"L'équipe\"   />\n" +
		"  <img src=\"/static/logo.svg\" alt=\"Logo\">\n" +
		"  <p>Si a < b alors &lt;img&gt; ; prix : 10 € <br/>  fin </p>\n" +
		"  <svg viewBox=\"0 0 10 10\"><path d=\"M0 0L10 10\"/></svg>\n" +
		"  <img\n     data-photo=\"mountain lake\"\n     src=\"https://placehold.co/400x600\"\n  >\n" +
		"  <script>document.write('<img data-photo=\"x\" src=\"y\">')</script>\n" +
		"</section>\n\n  "

	s := NewService(&fakeProvider{fn: manyPhotos})
	got, n := s.Resolve(context.Background(), fragment)
	if n != 2 {
		t.Fatalf("n = %d, attendu 2", n)
	}

	// Même nombre de balises, et tout ce qui les sépare est identique.
	if a, b := imgTagRe.Split(fragment, -1), imgTagRe.Split(got, -1); strings.Join(a, "\x00") != strings.Join(b, "\x00") {
		t.Errorf("le texte hors <img> a changé :\n avant %q\n après %q", a, b)
	}
	before, after := imgTagRe.FindAllString(fragment, -1), imgTagRe.FindAllString(got, -1)
	if len(before) != len(after) {
		t.Fatalf("nombre de balises <img> : %d → %d", len(before), len(after))
	}
	changed := 0
	for i := range before {
		if before[i] != after[i] {
			changed++
			if !strings.Contains(after[i], "https://img.test/") || !strings.Contains(after[i], "data-photo-credit=") {
				t.Errorf("balise modifiée sans photo : %q", after[i])
			}
		}
	}
	if changed != 2 {
		t.Errorf("%d balises modifiées, attendu 2 (logo, commentaire et script intacts)", changed)
	}
	// Les ajouts se placent après le dernier attribut : l'espacement final et « /> » restent tels quels.
	if !strings.Contains(got, `ALT="L'équipe"`+testTailEnd+`   />`) || !strings.Contains(got, "loading=\"lazy\"\n  >") {
		t.Errorf("fin de balise altérée : %q", got)
	}
}

func TestInferOrientation(t *testing.T) {
	cases := map[string]string{
		"https://placehold.co/1200x800/1e293b/94a3b8?text=Equipe": "landscape",
		"https://placehold.co/800x1200":                           "portrait",
		"https://placehold.co/600x600.png":                        "square",
		"https://placehold.co/600x500":                            "square", // ratio 1,2 pile : pas encore paysage
		"https://placehold.co/601x500":                            "landscape",
		"https://placehold.co/400x500":                            "square", // ratio 0,8 pile
		"https://placehold.co/399x500":                            "portrait",
		"https://placehold.co/600":                                "square", // taille unique ⇒ carré
		"https://placehold.co/600/png":                            "square",
		"https://placehold.co/1600x900@2x.png?text=Hero":          "landscape",
		"//PLACEHOLD.CO/1200X400":                                 "landscape",
		"https://placehold.co/96x96":                              "square", // avatar
		"https://placehold.co/160x100":                            "square", // petit ⇒ carré malgré le ratio
		"https://placehold.co/100x160":                            "square",
		"https://placehold.co/161x100":                            "landscape",
		"https://placehold.co/0x100":                              "",
		"https://placehold.co/1e293b":                             "", // une couleur n'est pas une taille
		"https://images.pexels.com/photos/1/a.jpeg?w=1200&h=800":  "",
		"/static/hero-1200x800.jpg":                               "",
		"":                                                        "",
	}
	for src, want := range cases {
		if got := inferOrientation(src); got != want {
			t.Errorf("inferOrientation(%q) = %q, attendu %q", src, got, want)
		}
	}
}

func TestResolveSearchParameters(t *testing.T) {
	fragment := `<img src="https://placehold.co/1200x800" data-photo="  Startup   TEAM ">` +
		`<img src="https://placehold.co/400x600" data-photo="woman portrait">` +
		`<img src="https://placehold.co/96x96" data-photo="man face" class="rounded-full">` +
		`<img data-photo="city skyline">` + // sans src : à résoudre, orientation inconnue
		`<img src="/uploads/photo.jpg" data-photo="harbor">` // image réelle : on n'y touche pas
	f := &fakeProvider{fn: manyPhotos}
	if _, n := NewService(f).Resolve(context.Background(), fragment); n != 4 {
		t.Fatalf("n = %d, attendu 4", n)
	}
	got := map[string]fakeCall{}
	for _, c := range f.log() {
		got[c.query] = c
	}
	want := []fakeCall{
		{"startup team", "landscape", 8},
		{"woman portrait", "portrait", 8},
		{"man face", "square", 8},
		{"city skyline", "", 8},
	}
	if len(got) != len(want) {
		t.Fatalf("appels = %+v", f.log())
	}
	for _, w := range want {
		if got[w.query] != w {
			t.Errorf("appel %q = %+v, attendu %+v", w.query, got[w.query], w)
		}
	}
}

var srcRe = regexp.MustCompile(`src="([^"]*)"`)

func TestResolveNoDuplicatePhotos(t *testing.T) {
	img := `<img src="https://placehold.co/600x400" data-photo="office desk">`
	f := &fakeProvider{fn: manyPhotos}
	got, n := NewService(f).Resolve(context.Background(), strings.Repeat(img+"\n", 8))
	if n != 8 {
		t.Fatalf("n = %d, attendu 8", n)
	}
	if f.count() != 1 {
		t.Errorf("appels = %d, attendu 1 (même requête, même orientation)", f.count())
	}
	seen := map[string]bool{}
	for _, m := range srcRe.FindAllStringSubmatch(got, -1) {
		if seen[m[1]] {
			t.Errorf("photo réutilisée alors qu'il restait des alternatives : %s", m[1])
		}
		seen[m[1]] = true
	}
	if len(seen) != 8 {
		t.Errorf("%d photos distinctes, attendu 8", len(seen))
	}

	// Moins de photos que d'images : on réutilise plutôt que de laisser un placeholder.
	two := &fakeProvider{fn: func(q, o string, _ int) ([]Photo, error) { return manyPhotos(q, o, 2) }}
	got, n = NewService(two).Resolve(context.Background(), strings.Repeat(img, 5))
	if n != 5 || strings.Contains(got, "placehold.co") {
		t.Errorf("n = %d, attendu 5 sans placeholder restant : %q", n, got)
	}
}

// Même photo renvoyée pour deux requêtes différentes : elle ne sert qu'une fois.
func TestResolveNoDuplicateAcrossQueries(t *testing.T) {
	shared := func(string, string, int) ([]Photo, error) {
		return []Photo{
			{ID: "A", Provider: "fake", URL: "https://img.test/A.jpg"},
			{ID: "B", Provider: "fake", URL: "https://img.test/B.jpg"},
		}, nil
	}
	fragment := `<img src="https://placehold.co/600x400" data-photo="first query">` +
		`<img src="https://placehold.co/600x400" data-photo="second query">`
	got, n := NewService(&fakeProvider{fn: shared}).Resolve(context.Background(), fragment)
	if n != 2 || !strings.Contains(got, "A.jpg") || !strings.Contains(got, "B.jpg") {
		t.Errorf("n=%d, attendu A et B utilisées une fois chacune : %q", n, got)
	}
}

func TestResolveDeterministicAndVaried(t *testing.T) {
	fragment := `<img src="https://placehold.co/600x400" data-photo="office desk">` +
		`<p>texte</p><img src="https://placehold.co/600x400" data-photo="mountain lake">` +
		`<img src="https://placehold.co/600x400" data-photo="office desk">`
	first, n := NewService(&fakeProvider{fn: manyPhotos}).Resolve(context.Background(), fragment)
	if n != 3 {
		t.Fatalf("n = %d", n)
	}
	for range 5 {
		// Nouveau service (cache vide), même résultat.
		if again, _ := NewService(&fakeProvider{fn: manyPhotos}).Resolve(context.Background(), fragment); again != first {
			t.Fatalf("résultat non déterministe :\n %q\n %q", first, again)
		}
	}
	// Le choix suit hash(requête) + ordinal, pas toujours le 1er résultat.
	used := map[string]bool{}
	for ordinal, q := range []string{"office desk", "mountain lake", "office desk"} {
		photos, _ := manyPhotos(q, "", 8)
		p, ok := pickPhoto(photos, q, ordinal, used)
		if !ok || !strings.Contains(first, `src="`+p.URL+`"`) {
			t.Errorf("image %d : photo attendue %q absente de %q", ordinal, p.URL, first)
		}
	}
	picks := map[string]bool{}
	for _, q := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		photos, _ := manyPhotos("x", "", 8)
		p, _ := pickPhoto(photos, q, 0, map[string]bool{})
		picks[p.ID] = true
	}
	if len(picks) < 3 {
		t.Errorf("choix trop peu variés selon la requête : %v", picks)
	}
}

func TestResolveFailedSearchKeepsPlaceholder(t *testing.T) {
	f := &fakeProvider{fn: func(q, o string, n int) ([]Photo, error) {
		switch q {
		case "broken query":
			return nil, errors.New("panne réseau")
		case "no result":
			return nil, nil
		case "no url":
			return []Photo{{ID: "x", Provider: "fake"}}, nil
		}
		return manyPhotos(q, o, n)
	}}
	broken := `<img src="https://placehold.co/600x400" data-photo="broken query" alt="A">`
	empty := `<img src="https://placehold.co/600x400" data-photo="no result" alt="B">`
	noURL := `<img src="https://placehold.co/600x400" data-photo="no url" alt="C">`
	ok := `<img src="https://placehold.co/600x400" data-photo="office desk" alt="D">`
	got, n := NewService(f).Resolve(context.Background(), broken+empty+ok+noURL)
	if n != 1 {
		t.Fatalf("n = %d, attendu 1", n)
	}
	if !strings.HasPrefix(got, broken+empty+`<img src="https://img.test/office-desk/`) || !strings.HasSuffix(got, noURL) {
		t.Errorf("les images en échec doivent garder leur balise intacte : %q", got)
	}
}

func TestResolveDisabledIsNoOp(t *testing.T) {
	fragment := `<div><img src="https://placehold.co/600x400" data-photo="office desk"></div>`
	var nilService *Service
	for name, s := range map[string]*Service{"désactivé": NewService(nil), "nil": nilService, "FromEnv vide": FromEnv(func(string) string { return "" })} {
		if got, n := s.Resolve(context.Background(), fragment); got != fragment || n != 0 {
			t.Errorf("%s : n=%d got=%q", name, n, got)
		}
	}
}

func TestResolveCapsAtTwelveImages(t *testing.T) {
	var b strings.Builder
	for i := range 15 {
		fmt.Fprintf(&b, `<img src="https://placehold.co/600x400" data-photo="topic number %d">`+"\n", i)
	}
	fragment := b.String()
	f := &fakeProvider{fn: manyPhotos}
	got, n := NewService(f).Resolve(context.Background(), fragment)
	if n != 12 {
		t.Fatalf("n = %d, attendu 12", n)
	}
	if f.count() != 12 {
		t.Errorf("recherches = %d, attendu 12", f.count())
	}
	if c := strings.Count(got, "placehold.co"); c != 3 {
		t.Errorf("%d placeholders restants, attendu 3", c)
	}
	// Ce sont les 3 dernières qui restent intactes.
	tail := fragment[strings.Index(fragment, `<img src="https://placehold.co/600x400" data-photo="topic number 12">`):]
	if !strings.HasSuffix(got, tail) {
		t.Errorf("la fin du fragment devait rester identique : %q", got)
	}
	// Les images sans data-photo ne consomment pas le plafond.
	logo := `<img src="/logo.svg" alt="Logo">`
	if _, n := NewService(&fakeProvider{fn: manyPhotos}).Resolve(context.Background(), strings.Repeat(logo, 20)+fragment); n != 12 {
		t.Errorf("avec 20 logos devant : n = %d, attendu 12", n)
	}
}

func TestResolveEscaping(t *testing.T) {
	f := &fakeProvider{fn: func(string, string, int) ([]Photo, error) {
		return []Photo{{
			ID: "1", Provider: "fake",
			URL:    `https://img.test/a.jpg?x=1&y=2&z="q"<'>`,
			Author: `Tom "T" <b> & Co's`,
		}}, nil
	}}
	for _, quote := range []string{`"`, `'`} {
		in := `<img src=` + quote + `https://placehold.co/600x400` + quote + ` data-photo="office desk">`
		got, n := NewService(f).Resolve(context.Background(), in)
		want := `<img src=` + quote + `https://img.test/a.jpg?x=1&amp;y=2&amp;z=&#34;q&#34;&lt;&#39;&gt;` + quote +
			` data-photo="office desk" data-photo-credit="Tom &#34;T&#34; &lt;b&gt; &amp; Co&#39;s — fake" loading="lazy">`
		if n != 1 || got != want {
			t.Errorf("n=%d\n reçu    %q\n attendu %q", n, got, want)
		}
		// La balise produite se relit proprement : mêmes attributs, valeurs décodées intactes.
		tag, ok := parseTag(got, 0)
		if !ok || tag.end != len(got) || len(tag.attrs) != 4 {
			t.Fatalf("balise produite illisible : %q", got)
		}
	}
	// Sans auteur, le crédit se réduit au fournisseur.
	anon := &fakeProvider{fn: func(string, string, int) ([]Photo, error) {
		return []Photo{{ID: "1", Provider: "fake", URL: "https://img.test/a.jpg", Author: "  "}}, nil
	}}
	if got, _ := NewService(anon).Resolve(context.Background(), `<img src="https://placehold.co/80" data-photo="cat">`); !strings.Contains(got, `data-photo-credit="fake"`) {
		t.Errorf("crédit sans auteur : %q", got)
	}
}

func TestResolveIdempotentOnSecondPass(t *testing.T) {
	s := NewService(&fakeProvider{fn: singlePhoto})
	in := `<div><img src="https://placehold.co/600x400" data-photo="office desk" alt="Bureau"></div>`
	once, _ := s.Resolve(context.Background(), in)
	// Une image déjà réelle n'est plus jamais re-résolue (le modèle renvoie les
	// src existants quand on itère sur un bloc) : la seconde passe est un no-op.
	twice, n := s.Resolve(context.Background(), once)
	if n != 0 || twice != once {
		t.Errorf("seconde passe : n=%d\n 1re %q\n 2e  %q", n, once, twice)
	}
}

func TestResolveContextCancelled(t *testing.T) {
	fragment := `<img src="https://placehold.co/600x400" data-photo="office desk">`
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeProvider{fn: manyPhotos}
	if got, n := NewService(f).Resolve(ctx, fragment); got != fragment || n != 0 || f.count() != 0 {
		t.Errorf("contexte annulé : n=%d appels=%d got=%q", n, f.count(), got)
	}

	// Annulation en cours de route : ce qui est déjà trouvé est appliqué, le
	// reste garde son placeholder, et plus aucune recherche ne part.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	f = &fakeProvider{fn: func(q, o string, n int) ([]Photo, error) {
		cancel()
		return manyPhotos(q, o, n)
	}}
	var b strings.Builder
	for i := range 10 {
		fmt.Fprintf(&b, `<img src="https://placehold.co/600x400" data-photo="topic %d">`, i)
	}
	_, n := NewService(f).Resolve(ctx, b.String())
	if n < 1 || n > resolveParallel || f.count() > resolveParallel {
		t.Errorf("annulation en route : n=%d appels=%d (max %d)", n, f.count(), resolveParallel)
	}
}

// Clé refusée ou quota épuisé : inutile d'enchaîner 12 requêtes vouées à l'échec.
func TestResolveStopsOnFatalProviderError(t *testing.T) {
	f := &fakeProvider{fn: func(string, string, int) ([]Photo, error) {
		return nil, &apiError{provider: "fake", status: 429, quota: true}
	}}
	var b strings.Builder
	for i := range 12 {
		fmt.Fprintf(&b, `<img src="https://placehold.co/600x400" data-photo="topic %d">`, i)
	}
	got, n := NewService(f).Resolve(context.Background(), b.String())
	if n != 0 || got != b.String() {
		t.Errorf("n=%d, fragment modifié", n)
	}
	if f.count() > resolveParallel {
		t.Errorf("recherches = %d, attendu au plus %d", f.count(), resolveParallel)
	}
}

// Bout en bout : Resolve → Service → Pexels → serveur HTTP de test.
func TestResolveEndToEndWithPexels(t *testing.T) {
	srv, reqs := stubServer(t, 200, pexelsBody, nil)
	s := NewService(NewPexels("k", srv.URL, nil))
	in := `<img src="https://placehold.co/1200x800" data-photo="startup team" alt="Équipe">`
	got, n := s.Resolve(context.Background(), in)
	if n != 1 || !strings.Contains(got, `src="https://images.pexels.com/photos/`) || !strings.Contains(got, ` — pexels"`) {
		t.Fatalf("n=%d got=%q", n, got)
	}
	if strings.Contains(got, "&cs=") || strings.Contains(got, "&h=") {
		t.Errorf("« & » non échappé dans l'URL : %q", got)
	}
	if r := reqs.get(); r.param("orientation") != "landscape" || r.param("per_page") != "8" || r.param("query") != "startup team" {
		t.Errorf("requête = %+v", r.query)
	}
	s.Resolve(context.Background(), in)
	if reqs.count() != 1 {
		t.Errorf("appels HTTP = %d, attendu 1 (cache)", reqs.count())
	}
}
