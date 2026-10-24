package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func i64(v int64) *int64 { return &v }

func TestCreateGetList(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	p, err := s.Create(ctx, "a@x.fr", ProjectInput{
		Name:  "  Nébula  ",
		Brand: json.RawMessage(`{"name":"Nébula","colors":"violet"}`),
		Blocks: []BlockInput{
			{ID: "b1", Type: "hero", HTML: "<section>1</section>", Prompt: "hero sombre"},
			{ID: "b1", Type: "footer", HTML: "<footer>2</footer>"}, // doublon ⇒ nouvel id
			{Type: "cta"}, // sans id ni HTML ⇒ accepté, pas de version
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Nébula" || p.Owner != "a@x.fr" || p.Rev != 1 || len(p.Blocks) != 3 {
		t.Fatalf("projet inattendu : %+v", p)
	}
	if p.Blocks[0].ID != "b1" || p.Blocks[1].ID == "b1" || p.Blocks[2].ID == "" {
		t.Fatalf("identifiants de blocs : %+v", p.Blocks)
	}
	if p.Blocks[0].VersionID == 0 || p.Blocks[2].VersionID != 0 {
		t.Fatalf("un bloc avec HTML doit avoir une version, un bloc vide non : %+v", p.Blocks)
	}
	if string(p.Theme) != "{}" {
		t.Fatalf("thème par défaut : %s", p.Theme)
	}

	if _, err := s.Create(ctx, "b@x.fr", ProjectInput{}); err != nil {
		t.Fatal(err)
	}
	mine, _ := s.List(ctx, "a@x.fr", false)
	all, _ := s.List(ctx, "a@x.fr", true)
	if len(mine) != 1 || mine[0].Blocks != 3 || len(all) != 2 {
		t.Fatalf("listes : mine=%+v all=%+v", mine, all)
	}
	if _, err := s.Get(ctx, "inconnu00"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, reçu %v", err)
	}
}

func TestValidation(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	cases := []ProjectInput{
		{Brand: json.RawMessage(`[1,2]`)},                                            // pas un objet
		{Theme: json.RawMessage(`{"x":"` + strings.Repeat("a", MaxJSONBlob) + `"}`)}, // trop gros
		{Blocks: []BlockInput{{ID: "ok", Type: "Hero!"}}},                            // type invalide
		{Blocks: []BlockInput{{ID: "../x", Type: "hero"}}},                           // id invalide
		{Blocks: []BlockInput{{ID: "b", Type: "hero", HTML: strings.Repeat("x", MaxBlockHTML+1)}}},
	}
	for i, in := range cases {
		if _, err := s.Create(ctx, "a@x.fr", in); !errors.Is(err, ErrInvalid) {
			t.Errorf("cas %d : attendu ErrInvalid, reçu %v", i, err)
		}
	}
	many := make([]BlockInput, MaxBlocksPerProject+1)
	for i := range many {
		many[i] = BlockInput{ID: fmt.Sprintf("b%d", i), Type: "hero"}
	}
	if _, err := s.Create(ctx, "a@x.fr", ProjectInput{Blocks: many}); !errors.Is(err, ErrLimit) {
		t.Errorf("attendu ErrLimit, reçu %v", err)
	}
}

func TestSaveBlockVersionsAndUndo(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	p, _ := s.Create(ctx, "a@x.fr", ProjectInput{})
	save := func(html, src string) Block {
		t.Helper()
		b, _, err := s.SaveBlock(ctx, p.ID, BlockInput{ID: "b1", Type: "hero", HTML: html, Prompt: "p-" + html, Source: src}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	b := save("", SourceEdit) // bloc vide : pas de version
	if b.VersionID != 0 || b.CanUndo || b.CanRedo {
		t.Fatalf("bloc vide : %+v", b)
	}
	save("v1", SourceGenerate)
	save("v1", SourceGenerate) // identique : pas de doublon
	save("v2", SourceCode)
	b = save("v3", "nimporte") // source inconnue ⇒ edit
	if !b.CanUndo || b.CanRedo {
		t.Fatalf("après 3 versions : %+v", b)
	}
	vs, _ := s.Versions(ctx, p.ID, "b1")
	if len(vs) != 3 || vs[0].HTML != "v3" || !vs[0].Current || vs[0].Source != SourceEdit || vs[2].Source != SourceGenerate {
		t.Fatalf("versions : %+v", vs)
	}

	// Annuler deux fois, rétablir une fois.
	b, _, _ = s.Step(ctx, p.ID, "b1", -1, nil)
	b, _, _ = s.Step(ctx, p.ID, "b1", -1, nil)
	if b.HTML != "v1" || b.Prompt != "p-v1" || b.CanUndo || !b.CanRedo {
		t.Fatalf("après 2 annulations : %+v", b)
	}
	if _, _, err := s.Step(ctx, p.ID, "b1", -1, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("annuler en butée : %v", err)
	}
	b, _, _ = s.Step(ctx, p.ID, "b1", +1, nil)
	if b.HTML != "v2" {
		t.Fatalf("rétablir : %+v", b)
	}

	// Diverger depuis v2 : la version courante est recopiée en bout de liste,
	// puis la nouvelle s'empile — annuler ramène à v2, et v3 reste listée.
	b = save("v4", SourceGenerate)
	if b.CanRedo {
		t.Fatalf("après divergence, plus rien à rétablir : %+v", b)
	}
	b, _, _ = s.Step(ctx, p.ID, "b1", -1, nil)
	if b.HTML != "v2" {
		t.Fatalf("annuler après divergence doit revenir à v2, reçu %q", b.HTML)
	}
	vs, _ = s.Versions(ctx, p.ID, "b1")
	got := []string{}
	for _, v := range vs {
		got = append(got, v.HTML)
	}
	if strings.Join(got, ",") != "v4,v2,v3,v2,v1" {
		t.Fatalf("historique après divergence : %v", got)
	}

	// Restaurer une version précise.
	b, _, err := s.Checkout(ctx, p.ID, "b1", vs[len(vs)-1].ID, nil)
	if err != nil || b.HTML != "v1" {
		t.Fatalf("checkout : %+v %v", b, err)
	}
	if _, _, err := s.Checkout(ctx, p.ID, "b1", 99999, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("checkout d'une version inconnue : %v", err)
	}
}

func TestVersionPruning(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	p, _ := s.Create(ctx, "a@x.fr", ProjectInput{})
	for i := 0; i < KeepVersions+8; i++ {
		if _, _, err := s.SaveBlock(ctx, p.ID, BlockInput{ID: "b1", Type: "hero", HTML: fmt.Sprintf("v%d", i)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	vs, _ := s.Versions(ctx, p.ID, "b1")
	if len(vs) != KeepVersions || vs[0].HTML != fmt.Sprintf("v%d", KeepVersions+7) || !vs[0].Current {
		t.Fatalf("élagage : %d versions, tête %q", len(vs), vs[0].HTML)
	}
}

func TestUpdateMetaOrderAndRev(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	p, _ := s.Create(ctx, "a@x.fr", ProjectInput{Blocks: []BlockInput{
		{ID: "a", Type: "header", HTML: "A"}, {ID: "b", Type: "hero", HTML: "B"}, {ID: "c", Type: "footer", HTML: "C"},
	}})

	name := "Renommé"
	theme := json.RawMessage(`{"primary":"#7C3AED"}`)
	order := []string{"c", "a"} // b disparaît
	out, err := s.UpdateMeta(ctx, p.ID, MetaInput{Name: &name, Theme: &theme, Order: &order, Rev: i64(p.Rev)})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != name || out.Rev != p.Rev+1 || string(out.Theme) != `{"primary":"#7C3AED"}` {
		t.Fatalf("méta : %+v", out)
	}
	if len(out.Blocks) != 2 || out.Blocks[0].ID != "c" || out.Blocks[1].ID != "a" {
		t.Fatalf("ordre : %+v", out.Blocks)
	}
	if vs, _ := s.Versions(ctx, p.ID, "b"); len(vs) != 0 {
		t.Fatalf("les versions d'un bloc supprimé doivent partir en cascade : %+v", vs)
	}

	// Révision périmée ⇒ conflit, rien n'est écrit.
	stale := "Périmé"
	if _, err := s.UpdateMeta(ctx, p.ID, MetaInput{Name: &stale, Rev: i64(p.Rev)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("attendu ErrConflict, reçu %v", err)
	}
	if _, _, err := s.SaveBlock(ctx, p.ID, BlockInput{ID: "a", Type: "header", HTML: "A2"}, i64(p.Rev)); !errors.Is(err, ErrConflict) {
		t.Fatalf("attendu ErrConflict sur un bloc, reçu %v", err)
	}
	cur, _ := s.Get(ctx, p.ID)
	if cur.Name != name || cur.Blocks[1].HTML != "A" {
		t.Fatalf("un conflit ne doit rien écrire : %+v", cur)
	}

	// Un nouveau bloc enregistré seul se place en fin de page.
	if _, _, err := s.SaveBlock(ctx, p.ID, BlockInput{ID: "d", Type: "cta"}, i64(cur.Rev)); err != nil {
		t.Fatal(err)
	}
	cur, _ = s.Get(ctx, p.ID)
	if cur.Blocks[len(cur.Blocks)-1].ID != "d" {
		t.Fatalf("nouveau bloc en fin : %+v", cur.Blocks)
	}

	bad := []string{"a", "a"}
	if _, err := s.UpdateMeta(ctx, p.ID, MetaInput{Order: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ordre en double : %v", err)
	}
	empty := " "
	if _, err := s.UpdateMeta(ctx, p.ID, MetaInput{Name: &empty}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nom vide : %v", err)
	}
}

func TestDeleteCascadesAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "p.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Create(ctx, "a@x.fr", ProjectInput{Blocks: []BlockInput{{ID: "a", Type: "hero", HTML: "A"}}})
	keep, _ := s.Create(ctx, "a@x.fr", ProjectInput{Name: "Gardé"})
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double suppression : %v", err)
	}
	var n int
	s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM blocks) + (SELECT COUNT(*) FROM block_versions)`).Scan(&n)
	if n != 0 {
		t.Fatalf("cascade : %d lignes orphelines", n)
	}
	s.Close()

	// Réouverture : les migrations ne rejouent pas, les données sont là.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.Get(ctx, keep.ID); err != nil || got.Name != "Gardé" {
		t.Fatalf("réouverture : %+v %v", got, err)
	}
}

func TestTranslations(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	p, _ := s.Create(ctx, "a@x.fr", ProjectInput{})
	h1, h2 := "0123456789abcdef", "fedcba9876543210"

	empty, err := s.Translations(ctx, p.ID)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("mémoire vide : %v %v", empty, err)
	}
	err = s.SaveTranslations(ctx, p.ID, "en", []Translation{
		{Hash: h1, Src: "Bonjour", Text: "Hello", Status: StatusMachine},
		{Hash: h2, Src: "Lisez <1>ceci</1>", Text: "Read <1>this</1>", Status: StatusHuman},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTranslations(ctx, p.ID, "es", []Translation{{Hash: h1, Src: "Bonjour", Text: "Hola", Status: StatusMachine}}); err != nil {
		t.Fatal(err)
	}
	// Écrasement : la relecture humaine remplace la traduction machine.
	if err := s.SaveTranslations(ctx, p.ID, "en", []Translation{{Hash: h1, Src: "Bonjour", Text: "Hi", Status: StatusHuman}}); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Translations(ctx, p.ID)
	if got := m["en"][h1]; got.Text != "Hi" || got.Status != StatusHuman || got.Src != "Bonjour" || got.UpdatedAt == "" {
		t.Fatalf("upsert : %+v", got)
	}
	if len(m["en"]) != 2 || m["es"][h1].Text != "Hola" {
		t.Fatalf("mémoire : %+v", m)
	}
	// Texte vide ⇒ suppression.
	if err := s.SaveTranslations(ctx, p.ID, "en", []Translation{{Hash: h2}}); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Translations(ctx, p.ID); len(m["en"]) != 1 {
		t.Fatalf("suppression : %+v", m["en"])
	}
	// Les traductions ne touchent pas à la révision du projet.
	if got, _ := s.Get(ctx, p.ID); got.Rev != p.Rev {
		t.Fatalf("rev modifiée : %d → %d", p.Rev, got.Rev)
	}

	// Validation.
	long := strings.Repeat("x", MaxTranslationLen+1)
	for name, c := range map[string]struct {
		lang string
		ts   []Translation
	}{
		"langue":      {"EN", []Translation{{Hash: h1, Text: "x", Status: StatusHuman}}},
		"langue path": {"../x", nil},
		"hash":        {"en", []Translation{{Hash: "XYZ", Text: "x", Status: StatusHuman}}},
		"statut":      {"en", []Translation{{Hash: h1, Text: "x", Status: "draft"}}},
		"longueur":    {"en", []Translation{{Hash: h1, Text: long, Status: StatusHuman}}},
		"lot":         {"en", make([]Translation, MaxTranslationsPerOp+1)},
	} {
		if err := s.SaveTranslations(ctx, p.ID, c.lang, c.ts); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : attendu ErrInvalid, obtenu %v", name, err)
		}
	}
	if err := s.SaveTranslations(ctx, "inconnu00", "en", []Translation{{Hash: h1, Text: "x", Status: StatusHuman}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("projet inconnu : %v", err)
	}

	// Cascade.
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM translations`).Scan(&n)
	if n != 0 {
		t.Fatalf("cascade : %d traductions orphelines", n)
	}
}

func TestTranslationsCap(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	p, _ := s.Create(ctx, "a@x.fr", ProjectInput{})
	batch := func(from, n int) []Translation {
		ts := make([]Translation, n)
		for i := range ts {
			ts[i] = Translation{Hash: fmt.Sprintf("%016x", from+i), Src: "s", Text: "t", Status: StatusMachine}
		}
		return ts
	}
	for i := 0; i < MaxTranslationsPerPrj; i += MaxTranslationsPerOp {
		if err := s.SaveTranslations(ctx, p.ID, "en", batch(i, MaxTranslationsPerOp)); err != nil {
			t.Fatal(err)
		}
	}
	// Au plafond : une ligne de plus est refusée et le lot entier annulé.
	err := s.SaveTranslations(ctx, p.ID, "de", batch(0, 2))
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("plafond : %v", err)
	}
	if m, _ := s.Translations(ctx, p.ID); len(m["de"]) != 0 || len(m["en"]) != MaxTranslationsPerPrj {
		t.Fatalf("lot non annulé : de=%d en=%d", len(m["de"]), len(m["en"]))
	}
	// Une mise à jour d'une ligne existante reste possible au plafond.
	if err := s.SaveTranslations(ctx, p.ID, "en", []Translation{{Hash: fmt.Sprintf("%016x", 0), Src: "s", Text: "u", Status: StatusHuman}}); err != nil {
		t.Fatalf("mise à jour au plafond : %v", err)
	}
}

// Une base créée avant la migration 2 (user_version = 1) monte de version
// sans perdre ses projets.
func TestMigrationFromV1(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v1.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Create(ctx, "a@x.fr", ProjectInput{Name: "Ancien"})
	s.db.Exec(`DROP TABLE translations`)
	s.db.Exec(`PRAGMA user_version = 1`)
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != len(migrations) {
		t.Fatalf("user_version = %d", v)
	}
	if got, err := s.Get(ctx, p.ID); err != nil || got.Name != "Ancien" {
		t.Fatalf("projet perdu : %+v %v", got, err)
	}
	if err := s.SaveTranslations(ctx, p.ID, "en", []Translation{{Hash: "0123456789abcdef", Src: "a", Text: "b", Status: StatusHuman}}); err != nil {
		t.Fatal(err)
	}
}

func TestCounts(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	c, err := s.Counts(ctx)
	if err != nil || c.Projects != 0 || c.Versions == nil || c.Translations == nil || c.Versions[SourceGenerate] != 0 {
		t.Fatalf("vide : %+v %v", c, err)
	}
	p, _ := s.Create(ctx, "a@x.fr", ProjectInput{Blocks: []BlockInput{{ID: "a", Type: "hero", HTML: "<h1>Un</h1>"}}})
	s.SaveBlock(ctx, p.ID, BlockInput{ID: "a", Type: "hero", HTML: "<h1>Deux</h1>", Source: SourceVariant}, nil)
	s.Create(ctx, "b@x.fr", ProjectInput{})
	s.SaveTranslations(ctx, p.ID, "en", []Translation{{Hash: "0123456789abcdef", Src: "Un", Text: "One", Status: StatusMachine}})
	c, err = s.Counts(ctx)
	if err != nil || c.Projects != 2 || c.Blocks != 1 || c.Versions[SourceVariant] != 1 || c.Versions[SourceImport] != 1 || c.Translations[StatusMachine] != 1 {
		t.Fatalf("compteurs : %+v %v", c, err)
	}
}
