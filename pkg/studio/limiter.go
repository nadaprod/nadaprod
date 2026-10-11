package studio

import (
	"sync"
	"time"
)

// limiter : seau à jetons par compte — protège le quota Gemini (trois
// variantes en un clic = trois générations). Volontairement sans dépendance :
// une map et un mutex suffisent à l'échelle d'une instance.
type limiter struct {
	mu      sync.Mutex
	burst   float64
	refill  float64 // jetons par seconde
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	seen   time.Time
}

func newLimiter(perWindow int, window time.Duration) *limiter {
	return &limiter{
		burst:   float64(perWindow),
		refill:  float64(perWindow) / window.Seconds(),
		buckets: map[string]*bucket{},
		now:     time.Now,
	}
}

// take consomme un jeton. Renvoie 0 si la requête passe, sinon le délai avant
// qu'un jeton soit de nouveau disponible.
func (l *limiter) take(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) > 10000 { // comptes supprimés, clés oubliées : on repart de zéro
			l.buckets = map[string]*bucket{}
		}
		b = &bucket{tokens: l.burst, seen: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.seen).Seconds()*l.refill)
	b.seen = now
	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	return time.Duration((1 - b.tokens) / l.refill * float64(time.Second))
}
