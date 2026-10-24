package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// clock : horloge réglable pour les tests.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func testStore(t *testing.T) (*Store, *clock, string) {
	t.Helper()
	c := &clock{time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "stats.db")
	s, err := open(path, c.now, time.Hour) // pas d'écriture périodique pendant le test
	if err != nil {
		t.Fatal(err)
	}
	return s, c, path
}

func TestIsBot(t *testing.T) {
	for ua, want := range map[string]bool{
		"": true,
		"Mozilla/5.0 (compatible; Googlebot/2.1)": true,
		"curl/8.5.0":           true,
		"python-requests/2.31": true,
		"Mozilla/5.0 (X11; Linux x86_64) HeadlessChrome/120": true,
		"UptimeRobot/2.0":         true,
		"facebookexternalhit/1.1": true,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/155.0 Safari/537.36": false,
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Mobile/15E148 Safari/604.1":             false,
	} {
		if IsBot(ua) != want {
			t.Errorf("IsBot(%q) = %v", ua, !want)
		}
	}
}

func TestHitsAndSummary(t *testing.T) {
	s, c, path := testStore(t)
	ctx := context.Background()

	s.Hit("pp", 200, 10_000, 400*time.Microsecond, true, false)
	s.Hit("pp", 200, 2_000, 9*time.Millisecond, false, false) // asset : pas une page
	s.Hit("pp", 404, 500, time.Millisecond, true, false)      // erreur : pas une page
	s.Hit("pp", 200, 10_000, time.Millisecond, true, true)    // robot : compté à part
	s.Hit("l3dlp.com", 200, 4_000, 200*time.Microsecond, true, false)
	s.Hit("", 200, 1, time.Millisecond, true, false) // ignoré
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	// Deuxième passage le même jour : les compteurs s'additionnent.
	s.Hit("pp", 200, 1_000, 600*time.Microsecond, true, false)
	// Le lendemain, et un appel IA.
	c.t = c.t.Add(24 * time.Hour)
	s.Hit("pp", 200, 1_000, 300*time.Microsecond, true, false)
	s.AI(AIEvent{Kind: "hero", Model: "m", OK: true, PromptTokens: 100, OutputTokens: 900, Duration: 2 * time.Second})
	s.AI(AIEvent{Kind: "hero", Model: "m", OK: false, Duration: time.Second})
	s.AI(AIEvent{Kind: "translate", Model: "m", OK: true, PromptTokens: 10, OutputTokens: 20, Duration: 500 * time.Millisecond})

	sum, err := s.Summary(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	st := sum.Static
	if sum.Since != "2026-10-25" || st.Requests != 7 || st.Pages != 4 || st.Bots != 1 || st.Bytes != 28_500 {
		t.Fatalf("statique : since=%s %+v", sum.Since, st)
	}
	if st.PageUS != (400+200+600+300)/4 {
		t.Errorf("temps moyen d'une page : %d", st.PageUS)
	}
	if len(st.Daily) != 3 || st.Daily[0].Day != "2026-10-24" || st.Daily[0].Requests != 0 ||
		st.Daily[1] != (StaticDay{"2026-10-25", 6, 3}) || st.Daily[2] != (StaticDay{"2026-10-26", 1, 1}) {
		t.Errorf("série statique : %+v", st.Daily)
	}
	ai := sum.AI
	if ai.Requests != 3 || ai.Failures != 1 || ai.PromptTokens != 110 || ai.OutputTokens != 920 || ai.MS != 3500 {
		t.Fatalf("IA : %+v", ai)
	}
	if len(ai.ByKind) != 2 || ai.ByKind[0] != (KindCount{"hero", 2, 900}) || ai.ByKind[1].Kind != "translate" {
		t.Errorf("par nature : %+v", ai.ByKind)
	}
	if ai.Daily[2] != (AIDay{"2026-10-26", 3, 920}) || ai.Daily[1].Requests != 0 {
		t.Errorf("série IA : %+v", ai.Daily)
	}

	// Détail par site (root).
	sites, err := s.SiteSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 || sites[0].Site != "pp" || sites[0].Requests != 6 || sites[0].Pages != 3 || len(sites[0].Daily) != 30 ||
		sites[0].Daily[29] != (StaticDay{"2026-10-26", 1, 1}) {
		t.Fatalf("par site : %+v", sites)
	}

	// Close vide le tampon : rien n'est perdu à l'arrêt.
	s.Hit("pp", 200, 1, time.Microsecond, true, false)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", "file:"+path)
	defer db.Close()
	var n int
	db.QueryRow(`SELECT SUM(requests) FROM hits_daily`).Scan(&n)
	if n != 8 {
		t.Errorf("après Close : %d requêtes", n)
	}
	// Aucune colonne identifiante.
	for _, table := range []string{"hits_daily", "ai_daily"} {
		rows, _ := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		for rows.Next() {
			var col string
			rows.Scan(&col)
			switch col {
			case "ip", "ua", "user_agent", "url", "path", "referer":
				t.Errorf("%s.%s : donnée identifiante", table, col)
			}
		}
		rows.Close()
	}
}

func TestSummaryEmpty(t *testing.T) {
	s, _, _ := testStore(t)
	defer s.Close()
	sum, err := s.Summary(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(sum)
	var back map[string]any
	json.Unmarshal(raw, &back)
	ai, st := back["ai"].(map[string]any), back["static"].(map[string]any)
	if sum.Since != "2026-10-25" || ai["by_kind"] == nil || len(sum.AI.Daily) != 30 || st["daily"] == nil || sum.Static.PageUS != 0 {
		t.Fatalf("vide : %s", raw)
	}
}

func TestReopenKeepsData(t *testing.T) {
	s, c, path := testStore(t)
	s.AI(AIEvent{Kind: "faq", Model: "m", OK: true, OutputTokens: 5})
	s.Hit("x", 200, 1, time.Millisecond, true, false)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := open(path, c.now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	sum, _ := s2.Summary(context.Background(), 1)
	if sum.AI.Requests != 1 || sum.Static.Pages != 1 {
		t.Fatalf("réouverture : %+v", sum)
	}
}
