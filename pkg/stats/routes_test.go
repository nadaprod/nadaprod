package stats

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, _, _ := testStore(t)
	defer s.Close()
	s.Hit("pp", 200, 4096, 500*time.Microsecond, true, false)
	s.Hit("pp", 200, 1024, time.Millisecond, true, true)
	s.AI(AIEvent{Kind: "hero", Model: "gemini-3.7-flash", OK: true, PromptTokens: 1200, OutputTokens: 3400, Duration: 7 * time.Second})
	s.AI(AIEvent{Kind: "translate", Model: "gemini-3.7-flash", OK: true, PromptTokens: 300, OutputTokens: 280, Duration: 2 * time.Second})

	calls := 0
	r := gin.New()
	isRoot := false
	root := r.Group("/api", func(c *gin.Context) {
		if !isRoot {
			c.AbortWithStatusJSON(403, gin.H{"code": "forbidden"})
		}
	})
	Register(r.Group("/api"), root, Deps{Store: s, Counts: func(context.Context) (AppCounts, error) {
		calls++
		return AppCounts{Projects: 3, Blocks: 12, Versions: map[string]int{"generate": 20, "variant": 6}, Translations: map[string]int{"machine": 40, "human": 5},
			Sites: 30, LiveSites: 14, Pages: 210}, nil
	}})
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}

	w := get("/api/stats")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "max-age=60") {
		t.Fatalf("stats : %d %v", w.Code, w.Header())
	}
	var got struct {
		Since  string        `json:"since"`
		AI     AISummary     `json:"ai"`
		Static StaticSummary `json:"static"`
		App    AppCounts     `json:"app"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AI.Requests != 2 || got.AI.OutputTokens != 3680 || got.Static.Pages != 1 || got.Static.Bots != 1 || got.App.LiveSites != 14 || len(got.AI.Daily) != 30 {
		t.Fatalf("contenu : %s", w.Body)
	}
	if strings.Contains(w.Body.String(), `"pp"`) {
		t.Error("la réponse publique ne doit rien dire des sites")
	}
	t.Logf("GET /api/stats → %s", w.Body)

	// Cache : un deuxième appel ne recalcule pas.
	get("/api/stats")
	if calls != 1 {
		t.Errorf("cache : %d calculs", calls)
	}

	// Détail par site : root seulement.
	if w := get("/api/stats/sites"); w.Code != 403 {
		t.Errorf("détail sans root : %d", w.Code)
	}
	isRoot = true
	w = get("/api/stats/sites")
	var sites []SiteStats
	json.Unmarshal(w.Body.Bytes(), &sites)
	if w.Code != 200 || len(sites) != 1 || sites[0].Site != "pp" || sites[0].Requests != 2 {
		t.Fatalf("détail : %d %s", w.Code, w.Body)
	}
}
