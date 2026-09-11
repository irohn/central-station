package auth

import (
	"sync"
	"time"
)

// Limiter counts failures per key inside a sliding window and blocks a key
// once it has too many. Used to slow down password guessing.
type Limiter struct {
	Max    int
	Window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	hits  map[string][]time.Time
	sweep time.Time
}

// NewLimiter allows max failures per window per key.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{Max: max, Window: window, now: time.Now, hits: map[string][]time.Time{}}
}

func (l *Limiter) prune(key string, now time.Time) []time.Time {
	ts := l.hits[key]
	keep := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < l.Window {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.hits, key)
		return nil
	}
	l.hits[key] = keep
	return keep
}

// Blocked reports whether key has exhausted its failures.
func (l *Limiter) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.sweep) > l.Window { // occasional global sweep so idle keys don't pile up
		for k := range l.hits {
			l.prune(k, now)
		}
		l.sweep = now
	}
	return len(l.prune(key, now)) >= l.Max
}

// Fail records a failure for key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.hits[key] = append(l.prune(key, now), now)
}

// Reset clears failures for key (after a successful login).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
