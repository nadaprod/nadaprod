package sites

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSiteDates(t *testing.T) {
	st, err := NewStore(t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Nouveau site : la date de création est écrite dans site.json.
	before := time.Now().UTC().Add(-time.Second)
	id, err := st.newSite("neuf")
	if err != nil {
		t.Fatal(err)
	}
	created, err := time.Parse(time.RFC3339, st.meta(id).Created)
	if err != nil || created.Before(before) {
		t.Fatalf("created = %q (%v)", st.meta(id).Created, err)
	}

	// Ancien site sans date : le plus ancien fichier fait foi ; la dernière
	// édition est le fichier le plus récent, CSS compris.
	old := "ancien"
	dev := st.devDir(old)
	if err := os.MkdirAll(filepath.Join(dev, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel string, at time.Time) {
		p := filepath.Join(dev, rel)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	write("index.html", t0)
	write("contact.html", t0.Add(24*time.Hour))
	write("css/site.css", t0.Add(72*time.Hour))

	pages, sp, err := st.scan(old)
	if err != nil || len(pages) != 2 || pages[0] != "index.html" {
		t.Fatalf("pages = %v (%v)", pages, err)
	}
	c, u := siteDates(st.meta(old), sp)
	if c != "2025-03-01T10:00:00Z" || u != "2025-03-04T10:00:00Z" {
		t.Fatalf("created %q, updated %q", c, u)
	}

	// Un domaine branché ensuite ne fait pas perdre la date de création.
	meta := st.meta(id)
	meta.Domain = "neuf.test"
	if err := st.writeMeta(id, meta); err != nil {
		t.Fatal(err)
	}
	if st.meta(id).Created == "" {
		t.Fatal("date de création perdue")
	}

	// Site vide : dates inconnues, pas d'erreur.
	if _, sp, _ := st.scan(id); !sp.newest.IsZero() {
		t.Fatal("site vide daté")
	}
	if c, u := siteDates(Meta{}, span{}); c != "" || u != "" {
		t.Fatalf("site vide : %q %q", c, u)
	}
}
