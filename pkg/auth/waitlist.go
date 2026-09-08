package auth

import (
	"encoding/json"
	"os"
)

const maxWaitlist = 5000 // au-delà, on accepte sans stocker (anti-abus)

type WaitlistEntry struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`
	Date    string `json:"date"`
	IP      string `json:"ip"`
}

// ---------- Liste d'attente ----------

func (s *UserStore) waitlist() []WaitlistEntry {
	var entries []WaitlistEntry
	if b, err := os.ReadFile(s.wlPath); err == nil {
		_ = json.Unmarshal(b, &entries)
	}
	return entries
}

// AddToWaitlist ajoute une candidature (dédupliquée par email).
func (s *UserStore) AddToWaitlist(e WaitlistEntry) error {
	s.wlMu.Lock()
	defer s.wlMu.Unlock()
	entries := s.waitlist()
	if len(entries) >= maxWaitlist {
		return nil // accepté silencieusement : le fichier ne grossit plus
	}
	for _, old := range entries {
		if old.Email == e.Email {
			return nil // déjà inscrit : idempotent
		}
	}
	entries = append(entries, e)
	b, _ := json.MarshalIndent(entries, "", "  ")
	return os.WriteFile(s.wlPath, b, 0o600)
}

func truncate(s string, n int) string {
	r := []rune(s) // tronquer en runes : ne jamais couper un caractère UTF-8
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
