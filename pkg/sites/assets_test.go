package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageAssets(t *testing.T) {
	page := `<html><head>
	<link rel="stylesheet" href="css/style.css">
	<link href='./theme.css?v=3' rel='stylesheet'>
	<link rel="preload stylesheet" href="/global.css#x">
	<link rel="icon" href="favicon.css">
	<link rel="stylesheet" href="https://cdn.example/x.css">
	<link rel="stylesheet" href="//cdn.example/y.css">
	<script src="app.js"></script>
	<script src="../shared/lib.js" defer></script>
	<script src="/js/vendor.mjs"></script>
	<script src="data:text/javascript,1"></script>
	<script src="css/style.css"></script>
	<script src="app.js"></script>
	</head><body></body></html>`
	got := PageAssets([]byte(page), "blog/post.html")
	want := "blog/css/style.css blog/theme.css global.css blog/app.js shared/lib.js js/vendor.mjs"
	if strings.Join(got, " ") != want {
		t.Fatalf("\n reçu    %v\n attendu %s", got, want)
	}
	// Comme dans un navigateur, « .. » au-delà de la racine s'arrête à la racine
	// du site : la référence reste dans dev/ (et safeJoin veille derrière).
	if got := PageAssets([]byte(`<script src="../../../etc/x.js"></script>`), "index.html"); strings.Join(got, ",") != "etc/x.js" {
		t.Fatalf("résolution au-delà de la racine : %v", got)
	}
	for _, rel := range []string{"a.html", "x/y.css", "z.js", "m.webmanifest", "note.md"} {
		if !Editable(rel) {
			t.Errorf("%s devrait être éditable", rel)
		}
	}
	for _, rel := range []string{"logo.png", "font.ttf", "a.zip", "", "bin"} {
		if Editable(rel) {
			t.Errorf("%s ne devrait pas être éditable", rel)
		}
	}
}

func TestStoreAssets(t *testing.T) {
	st, _ := NewStore(t.TempDir(), "", "", "")
	id, _ := st.CreateSite("demo")
	dev := st.devDir(id)
	os.MkdirAll(filepath.Join(dev, "css"), 0o755)
	os.MkdirAll(filepath.Join(dev, "np-fonts"), 0o755)
	os.WriteFile(filepath.Join(dev, "index.html"), []byte(`<link rel="stylesheet" href="css/a.css"><script src="missing.js"></script><script src="b.js"></script>`), 0o644)
	os.WriteFile(filepath.Join(dev, "css", "a.css"), []byte("body{}"), 0o644)
	os.WriteFile(filepath.Join(dev, "b.js"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(dev, "other.css"), []byte("p{}"), 0o644)
	os.WriteFile(filepath.Join(dev, "np-fonts", "x.css"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dev, "logo.png"), []byte("png"), 0o644)
	ref, others, err := st.Assets(id, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(ref) != 2 || ref[0].Path != "css/a.css" || ref[0].Size != 6 || ref[1].Path != "b.js" {
		t.Errorf("référencés : %+v", ref)
	}
	if len(others) != 1 || others[0].Path != "other.css" {
		t.Errorf("autres : %+v", others)
	}
	if _, _, err := st.Assets(id, "absente.html"); err == nil {
		t.Error("page absente acceptée")
	}
	st.waitBuilds()
}
