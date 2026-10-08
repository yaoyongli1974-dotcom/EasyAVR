package openapi

import (
	"sync"
	"time"
)

// Limiter is an in-memory fixed-window rate limiter keyed by API key id.
type Limiter struct {
	mu      sync.Mutex
	windows map[uint]*window
	now     func() time.Time
}

type window struct {
	start time.Time
	count int
}

// NewLimiter creates a limiter.
func NewLimiter() *Limiter {
	return &Limiter{windows: map[uint]*window{}, now: time.Now}
}

// Allow records a request for keyID under a per-minute limit (0 = unlimited)
// and reports whether it is within the limit along with the remaining budget.
func (l *Limiter) Allow(keyID uint, limit int) (bool, int) {
	if limit <= 0 {
		return true, -1
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[keyID]
	if w == nil || now.Sub(w.start) >= time.Minute {
		w = &window{start: now}
		l.windows[keyID] = w
	}
	if w.count >= limit {
		return false, 0
	}
	w.count++
	return true, limit - w.count
}

// Reset clears counters for a key (e.g. after it is deleted).
func (l *Limiter) Reset(keyID uint) {
	l.mu.Lock()
	delete(l.windows, keyID)
	l.mu.Unlock()
}
