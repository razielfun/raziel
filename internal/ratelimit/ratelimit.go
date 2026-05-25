package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a simple sliding-window rate limiter keyed by string.
type Limiter struct {
	mu      sync.Mutex
	windows map[string][]time.Time
	limit   int
	window  time.Duration
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{
		windows: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
}

// Allow returns true if the key is within the rate limit.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	times := l.windows[key]
	// Drop expired entries
	i := 0
	for i < len(times) && times[i].Before(cutoff) {
		i++
	}
	times = times[i:]
	if len(times) >= l.limit {
		l.windows[key] = times
		return false
	}
	l.windows[key] = append(times, now)
	return true
}
