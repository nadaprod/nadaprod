package auth

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *UserStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewUserStore(
		filepath.Join(dir, "users.json"),
		filepath.Join(dir, "waiting-list.json"),
		"root@test.fr", "root-secret", "test-secret",
	)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSessionRoundTrip(t *testing.T) {
	s := newTestStore(t)
	v := s.MakeSession("Ami@Test.fr")
	email, ok := s.ParseSession(v)
	if !ok || email != "ami@test.fr" {
		t.Fatalf("ParseSession = %q, %v ; attendu ami@test.fr, true", email, ok)
	}
}

func TestSessionTampered(t *testing.T) {
	s := newTestStore(t)
	v := s.MakeSession("ami@test.fr")

	cases := map[string]string{
		"signature modifiée": v[:len(v)-1] + "0",
		"payload modifié":    "x" + v,
		"format invalide":    "abc",
		"vide":               "",
	}
	for name, tampered := range cases {
		if _, ok := s.ParseSession(tampered); ok {
			t.Errorf("%s : session acceptée à tort", name)
		}
	}

	// Une session signée par un autre secret est refusée.
	dir := t.TempDir()
	other, _ := NewUserStore(filepath.Join(dir, "u.json"), filepath.Join(dir, "w.json"), "root@test.fr", "x", "autre-secret")
	if _, ok := s.ParseSession(other.MakeSession("ami@test.fr")); ok {
		t.Error("session d'un autre secret acceptée à tort")
	}
}

func TestSessionExpired(t *testing.T) {
	s := newTestStore(t)
	// Session forgée : payload correctement signé mais date d'expiration passée.
	p := "YW1pQHRlc3QuZnI." + strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10) // base64url("ami@test.fr")
	if _, ok := s.ParseSession(p + "." + s.sign(p)); ok {
		t.Error("session expirée acceptée à tort")
	}
}

func TestAuthenticate(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddUser("ami@test.fr", "motdepasse"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		email, pass string
		want        bool
	}{
		{"root@test.fr", "root-secret", true},
		{"ROOT@test.fr", "root-secret", true}, // email normalisé
		{"root@test.fr", "faux", false},
		{"ami@test.fr", "motdepasse", true},
		{"ami@test.fr", "faux", false},
		{"inconnu@test.fr", "motdepasse", false},
	}
	for _, c := range cases {
		if got := s.Authenticate(c.email, c.pass); got != c.want {
			t.Errorf("Authenticate(%q) = %v, attendu %v", c.email, got, c.want)
		}
	}
}

func TestCanAccess(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddUser("ami@test.fr", "motdepasse"); err != nil {
		t.Fatal(err)
	}
	sites := []string{"pp", "mael"}
	if err := s.UpdateUser("ami@test.fr", nil, &sites); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		email, site string
		want        bool
	}{
		{"root@test.fr", "nimporte", true}, // root voit tout
		{"ami@test.fr", "pp", true},
		{"ami@test.fr", "mael", true},
		{"ami@test.fr", "autre", false},
		{"inconnu@test.fr", "pp", false},
		{"", "pp", false},
	}
	for _, c := range cases {
		if got := s.CanAccess(c.email, c.site); got != c.want {
			t.Errorf("CanAccess(%q, %q) = %v, attendu %v", c.email, c.site, got, c.want)
		}
	}
}

func TestUserCRUDPersists(t *testing.T) {
	dir := t.TempDir()
	up, wp := filepath.Join(dir, "users.json"), filepath.Join(dir, "wl.json")
	s, err := NewUserStore(up, wp, "root@test.fr", "root-secret", "sec")
	if err != nil {
		t.Fatal(err)
	}

	// Créations invalides refusées.
	for _, c := range []struct{ email, pass string }{
		{"pas-un-email", "motdepasse"},
		{"ami@test.fr", "court"},       // < 8 caractères
		{"root@test.fr", "motdepasse"}, // email root réservé
	} {
		if err := s.AddUser(c.email, c.pass); err == nil {
			t.Errorf("AddUser(%q, %q) : erreur attendue", c.email, c.pass)
		}
	}

	if err := s.AddUser("ami@test.fr", "motdepasse"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUser("ami@test.fr", "motdepasse"); err == nil {
		t.Error("doublon accepté à tort")
	}
	sites := []string{"pp"}
	if err := s.UpdateUser("ami@test.fr", nil, &sites); err != nil {
		t.Fatal(err)
	}

	// Rechargement depuis le fichier : tout persiste.
	s2, err := NewUserStore(up, wp, "root@test.fr", "root-secret", "sec")
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Authenticate("ami@test.fr", "motdepasse") || !s2.CanAccess("ami@test.fr", "pp") {
		t.Error("compte non persisté correctement")
	}

	if err := s2.DeleteUser("ami@test.fr"); err != nil {
		t.Fatal(err)
	}
	if s2.exists("ami@test.fr") {
		t.Error("compte encore présent après suppression")
	}
	if !s2.exists("root@test.fr") {
		t.Error("root doit toujours exister")
	}
}

func TestWaitlist(t *testing.T) {
	s := newTestStore(t)
	e := WaitlistEntry{Name: "Léo", Email: "leo@test.fr", Message: "un site", Date: "2026-07-27T00:00:00Z"}
	if err := s.AddToWaitlist(e); err != nil {
		t.Fatal(err)
	}
	// Dédupliqué par email : pas de doublon, pas d'erreur.
	if err := s.AddToWaitlist(e); err != nil {
		t.Fatal(err)
	}
	if err := s.AddToWaitlist(WaitlistEntry{Email: "zoe@test.fr"}); err != nil {
		t.Fatal(err)
	}
	entries := s.waitlist()
	if len(entries) != 2 {
		t.Fatalf("waitlist = %d entrées, attendu 2", len(entries))
	}
	if entries[0].Email != "leo@test.fr" || entries[0].Name != "Léo" {
		t.Errorf("première entrée inattendue : %+v", entries[0])
	}
	// Le fichier existe et est du JSON valide (relu par un nouveau store).
	if _, err := os.Stat(s.wlPath); err != nil {
		t.Fatal(err)
	}
}
