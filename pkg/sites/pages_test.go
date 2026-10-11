package sites

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPagesLifecycle(t *testing.T) {
	st, err := NewStore(t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.CreateSite("Mon Nouveau Site !")
	if err != nil || id != "mon-nouveau-site" {
		t.Fatalf("création : %q %v", id, err)
	}
	if id2, _ := st.CreateSite("Mon nouveau site"); id2 != "mon-nouveau-site-2" {
		t.Fatalf("collision non suffixée : %q", id2)
	}
	if id3, err := st.CreateSite("../x"); err != nil || id3 != "x" {
		t.Fatalf("nom assaini : %q %v", id3, err)
	}
	if pages, _ := st.listPages(id); pages == nil || len(pages) != 0 {
		t.Fatalf("site neuf : liste vide attendue (jamais nil, sinon null en JSON) : %#v", pages)
	}

	rel, err := st.SavePage(id, "index.html", BlankPage("Accueil"), false)
	if err != nil || rel != "index.html" {
		t.Fatalf("première page : %q %v", rel, err)
	}
	if _, err := st.SavePage(id, "index.html", []byte("x"), false); !errors.Is(err, ErrPageExists) {
		t.Fatalf("écrasement sans overwrite : %v", err)
	}
	if _, err := st.SavePage(id, "/blog/article.html", []byte("<p>a</p>"), false); err != nil {
		t.Fatalf("sous-dossier créé au besoin : %v", err)
	}
	for _, bad := range []string{"../evil.html", "notes.txt", "", "dir/"} {
		if _, err := st.SavePage(id, bad, []byte("x"), true); err == nil {
			t.Errorf("chemin accepté à tort : %q", bad)
		}
	}
	if _, err := st.SavePage("inconnu", "index.html", []byte("x"), true); err == nil {
		t.Error("site inexistant accepté")
	}

	got, err := st.ReadPage(id, "index.html")
	if err != nil || !strings.Contains(string(got), "<h1") {
		t.Fatalf("lecture : %v", err)
	}
	if _, err := st.ReadPage(id, "absente.html"); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("page absente : %v", err)
	}
	if out, err := st.CopyPage(id, "index.html", "copie.html"); err != nil || out != "copie.html" {
		t.Fatalf("copie : %q %v", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(st.devDir(id), "copie.html")); string(b) != string(got) {
		t.Error("la copie diffère de l'original")
	}
	if PageName(" À propos.HTML ") != "a-propos.html" || PageName("") != "site.html" {
		t.Errorf("PageName : %q %q", PageName(" À propos.HTML "), PageName(""))
	}
	if !strings.Contains(string(BlankPage("a<b>")), "a&lt;b&gt;") {
		t.Error("titre non échappé")
	}
	st.waitBuilds() // SavePage republie en arrière-plan : finir avant le nettoyage du TempDir
}
