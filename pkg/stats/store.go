// Package stats : la mesure de /debate (doc/STATS.md). Agrégats par jour —
// requêtes servies aux sites publiés, appels à l'IA — sans IP, user-agent,
// cookie ni URL : on compte, on ne trace pas.
package stats

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const dayLayout = "2006-01-02"

var migrations = []string{
	// 1 — schéma initial
	`CREATE TABLE hits_daily (
		day TEXT NOT NULL,
		site TEXT NOT NULL,
		requests INTEGER NOT NULL DEFAULT 0,
		pages INTEGER NOT NULL DEFAULT 0,
		bots INTEGER NOT NULL DEFAULT 0,
		bytes INTEGER NOT NULL DEFAULT 0,
		dur_us INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, site)
	);
	CREATE TABLE ai_daily (
		day TEXT NOT NULL,
		kind TEXT NOT NULL,
		model TEXT NOT NULL,
		requests INTEGER NOT NULL DEFAULT 0,
		failures INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		ms INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, kind, model)
	);`,
}

// FlushEvery : cadence d'écriture des visites tenues en mémoire.
const FlushEvery = 60 * time.Second

type hitKey struct{ day, site string }

// hitAgg : cumul en mémoire d'un (jour, site).
type hitAgg struct{ requests, pages, bots, bytes, durUS int64 }

type Store struct {
	db  *sql.DB
	now func() time.Time

	mu   sync.Mutex
	hits map[hitKey]*hitAgg

	stop chan struct{}
	done chan struct{}
}

// Open ouvre (ou crée) la base et lance l'écriture périodique des visites.
func Open(path string) (*Store, error) {
	return open(path, time.Now, FlushEvery)
}

func open(path string, now func() time.Time, every time.Duration) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, now: now, hits: map[hitKey]*hitAgg{}, stop: make(chan struct{}), done: make(chan struct{})}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration de la base stats : %w", err)
	}
	go s.loop(every)
	return s, nil
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d : %w", i+1, err)
		}
		// PRAGMA n'accepte pas de paramètre lié ; i est un entier maîtrisé.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loop(every time.Duration) {
	defer close(s.done)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_ = s.Flush()
		case <-s.stop:
			return
		}
	}
}

// Close arrête l'écriture périodique, vide le tampon des visites et ferme la base.
func (s *Store) Close() error {
	close(s.stop)
	<-s.done
	err := s.Flush()
	if cerr := s.db.Close(); err == nil {
		err = cerr
	}
	return err
}

func (s *Store) today() string { return s.now().UTC().Format(dayLayout) }

// ---------- Collecte ----------

// Hit compte une requête servie à un site publié. html : page HTML servie avec
// succès (ce qu'un CMS aurait recalculé) ; son temps de service entre dans la
// moyenne des pages. Rien d'identifiant n'est reçu ni stocké.
func (s *Store) Hit(site string, status int, bytes int64, dur time.Duration, html, bot bool) {
	if site == "" {
		return
	}
	k := hitKey{s.today(), site}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.hits[k]
	if a == nil {
		a = &hitAgg{}
		s.hits[k] = a
	}
	a.requests++
	a.bytes += max(bytes, 0)
	if bot {
		a.bots++
		return
	}
	if html && status >= 200 && status < 300 {
		a.pages++
		a.durUS += dur.Microseconds()
	}
}

// Flush écrit les visites en mémoire (cumul ajouté aux compteurs du jour).
func (s *Store) Flush() error {
	s.mu.Lock()
	pending := s.hits
	s.hits = map[hitKey]*hitAgg{}
	s.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err == nil {
		for k, a := range pending {
			if _, err = tx.Exec(`INSERT INTO hits_daily (day, site, requests, pages, bots, bytes, dur_us) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (day, site) DO UPDATE SET requests = requests + excluded.requests, pages = pages + excluded.pages,
				bots = bots + excluded.bots, bytes = bytes + excluded.bytes, dur_us = dur_us + excluded.dur_us`,
				k.day, k.site, a.requests, a.pages, a.bots, a.bytes, a.durUS); err != nil {
				break
			}
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	if err != nil {
		// Écriture ratée : le cumul revient en mémoire pour le prochain passage.
		s.mu.Lock()
		for k, a := range pending {
			if b := s.hits[k]; b != nil {
				b.requests += a.requests
				b.pages += a.pages
				b.bots += a.bots
				b.bytes += a.bytes
				b.durUS += a.durUS
			} else {
				s.hits[k] = a
			}
		}
		s.mu.Unlock()
	}
	return err
}

// AIEvent : un appel à l'IA, déjà mesuré par le client Gemini.
type AIEvent struct {
	Kind, Model                string
	OK                         bool
	PromptTokens, OutputTokens int
	Duration                   time.Duration
}

// AI écrit immédiatement un appel IA (peu nombreux, chacun compte).
func (s *Store) AI(e AIEvent) {
	if e.Kind == "" {
		e.Kind = "?"
	}
	failed := 0
	if !e.OK {
		failed = 1
	}
	_, _ = s.db.Exec(`INSERT INTO ai_daily (day, kind, model, requests, failures, prompt_tokens, output_tokens, ms) VALUES (?, ?, ?, 1, ?, ?, ?, ?)
		ON CONFLICT (day, kind, model) DO UPDATE SET requests = requests + 1, failures = failures + excluded.failures,
		prompt_tokens = prompt_tokens + excluded.prompt_tokens, output_tokens = output_tokens + excluded.output_tokens, ms = ms + excluded.ms`,
		s.today(), e.Kind, e.Model, failed, e.PromptTokens, e.OutputTokens, e.Duration.Milliseconds())
}

var botRe = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|curl|wget|python|httpclient|headless|monitor|uptime|preview|facebookexternalhit`)

// IsBot : robot, outil ou agent sans user-agent — compté à part, jamais stocké.
func IsBot(userAgent string) bool { return userAgent == "" || botRe.MatchString(userAgent) }

// ---------- Lecture ----------

type KindCount struct {
	Kind         string `json:"kind"`
	Requests     int64  `json:"requests"`
	OutputTokens int64  `json:"output_tokens"`
}

type AIDay struct {
	Day          string `json:"day"`
	Requests     int64  `json:"requests"`
	OutputTokens int64  `json:"output_tokens"`
}

type AISummary struct {
	Requests     int64       `json:"requests"`
	Failures     int64       `json:"failures"`
	PromptTokens int64       `json:"prompt_tokens"`
	OutputTokens int64       `json:"output_tokens"`
	MS           int64       `json:"ms"`
	ByKind       []KindCount `json:"by_kind"`
	Daily        []AIDay     `json:"daily"`
}

type StaticDay struct {
	Day      string `json:"day"`
	Requests int64  `json:"requests"`
	Pages    int64  `json:"pages"`
}

type StaticSummary struct {
	Requests int64       `json:"requests"`
	Pages    int64       `json:"pages"`
	Bots     int64       `json:"bots"`
	Bytes    int64       `json:"bytes"`
	PageUS   int64       `json:"page_us"` // temps moyen de service d'une page (µs)
	Daily    []StaticDay `json:"daily"`
}

type Summary struct {
	Since  string        `json:"since"`
	AI     AISummary     `json:"ai"`
	Static StaticSummary `json:"static"`
}

// days : les n derniers jours (UTC), le plus ancien d'abord.
func (s *Store) days(n int) []string {
	out := make([]string, n)
	today := s.now().UTC()
	for i := range n {
		out[i] = today.AddDate(0, 0, i-n+1).Format(dayLayout)
	}
	return out
}

// Summary : agrégats publics — totaux depuis le premier jour mesuré, séries
// des `days` derniers jours (jours vides à zéro). Le tampon des visites est
// écrit d'abord, pour que la lecture soit à jour.
func (s *Store) Summary(ctx context.Context, days int) (Summary, error) {
	_ = s.Flush()
	days = min(max(days, 1), 366)
	out := Summary{Since: s.today()}
	var since sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(day) FROM (SELECT day FROM hits_daily UNION ALL SELECT day FROM ai_daily)`).Scan(&since); err != nil {
		return out, err
	}
	if since.Valid && since.String < out.Since {
		out.Since = since.String
	}

	ai := &out.AI
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(requests),0), COALESCE(SUM(failures),0), COALESCE(SUM(prompt_tokens),0),
		COALESCE(SUM(output_tokens),0), COALESCE(SUM(ms),0) FROM ai_daily`).Scan(&ai.Requests, &ai.Failures, &ai.PromptTokens, &ai.OutputTokens, &ai.MS); err != nil {
		return out, err
	}
	ai.ByKind = []KindCount{}
	rows, err := s.db.QueryContext(ctx, `SELECT kind, SUM(requests), SUM(output_tokens) FROM ai_daily GROUP BY kind`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var k KindCount
		if err := rows.Scan(&k.Kind, &k.Requests, &k.OutputTokens); err != nil {
			rows.Close()
			return out, err
		}
		ai.ByKind = append(ai.ByKind, k)
	}
	rows.Close()
	sort.SliceStable(ai.ByKind, func(i, j int) bool {
		if ai.ByKind[i].Requests != ai.ByKind[j].Requests {
			return ai.ByKind[i].Requests > ai.ByKind[j].Requests
		}
		return ai.ByKind[i].Kind < ai.ByKind[j].Kind
	})

	st := &out.Static
	var durUS int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(requests),0), COALESCE(SUM(pages),0), COALESCE(SUM(bots),0),
		COALESCE(SUM(bytes),0), COALESCE(SUM(dur_us),0) FROM hits_daily`).Scan(&st.Requests, &st.Pages, &st.Bots, &st.Bytes, &durUS); err != nil {
		return out, err
	}
	if st.Pages > 0 {
		st.PageUS = durUS / st.Pages
	}

	span := s.days(days)
	aiDay := map[string]*AIDay{}
	stDay := map[string]*StaticDay{}
	ai.Daily = make([]AIDay, len(span))
	st.Daily = make([]StaticDay, len(span))
	for i, d := range span {
		ai.Daily[i] = AIDay{Day: d}
		st.Daily[i] = StaticDay{Day: d}
		aiDay[d], stDay[d] = &ai.Daily[i], &st.Daily[i]
	}
	if err := scanDays(ctx, s.db, `SELECT day, SUM(requests), SUM(output_tokens) FROM ai_daily WHERE day >= ? GROUP BY day`, span[0],
		func(d string, a, b int64) {
			if p := aiDay[d]; p != nil {
				p.Requests, p.OutputTokens = a, b
			}
		}); err != nil {
		return out, err
	}
	err = scanDays(ctx, s.db, `SELECT day, SUM(requests), SUM(pages) FROM hits_daily WHERE day >= ? GROUP BY day`, span[0],
		func(d string, a, b int64) {
			if p := stDay[d]; p != nil {
				p.Requests, p.Pages = a, b
			}
		})
	return out, err
}

func scanDays(ctx context.Context, db *sql.DB, q, from string, fn func(day string, a, b int64)) error {
	rows, err := db.QueryContext(ctx, q, from)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var a, b int64
		if err := rows.Scan(&d, &a, &b); err != nil {
			return err
		}
		fn(d, a, b)
	}
	return rows.Err()
}

// SiteStats : visites d'un site (détail root).
type SiteStats struct {
	Site     string      `json:"site"`
	Requests int64       `json:"requests"`
	Pages    int64       `json:"pages"`
	Bots     int64       `json:"bots"`
	Bytes    int64       `json:"bytes"`
	PageUS   int64       `json:"page_us"`
	Daily    []StaticDay `json:"daily"`
}

// SiteSummary : totaux par site (le plus visité d'abord) et ses 30 derniers jours.
func (s *Store) SiteSummary(ctx context.Context) ([]SiteStats, error) {
	_ = s.Flush()
	out := []SiteStats{}
	rows, err := s.db.QueryContext(ctx, `SELECT site, SUM(requests), SUM(pages), SUM(bots), SUM(bytes), SUM(dur_us) FROM hits_daily
		GROUP BY site ORDER BY SUM(requests) DESC, site`)
	if err != nil {
		return out, err
	}
	index := map[string]int{}
	for rows.Next() {
		var st SiteStats
		var dur int64
		if err := rows.Scan(&st.Site, &st.Requests, &st.Pages, &st.Bots, &st.Bytes, &dur); err != nil {
			rows.Close()
			return out, err
		}
		if st.Pages > 0 {
			st.PageUS = dur / st.Pages
		}
		index[st.Site] = len(out)
		out = append(out, st)
	}
	rows.Close()
	span := s.days(30)
	for i := range out {
		out[i].Daily = make([]StaticDay, len(span))
		for j, d := range span {
			out[i].Daily[j] = StaticDay{Day: d}
		}
	}
	pos := map[string]int{}
	for j, d := range span {
		pos[d] = j
	}
	rows, err = s.db.QueryContext(ctx, `SELECT site, day, requests, pages FROM hits_daily WHERE day >= ?`, span[0])
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var site, day string
		var req, pages int64
		if err := rows.Scan(&site, &day, &req, &pages); err != nil {
			return out, err
		}
		if i, ok := index[site]; ok {
			if j, ok := pos[day]; ok {
				out[i].Daily[j] = StaticDay{Day: day, Requests: req, Pages: pages}
			}
		}
	}
	return out, rows.Err()
}
