package auth

import (
	"sync"
	"time"
)

// SlidingWindowLimiter implements a per-API-key sliding window rate limiter.
type SlidingWindowLimiter struct {
	mu      sync.Mutex
	windows map[string][]time.Time // keyID -> timestamps of recent requests
}

// NewSlidingWindowLimiter creates a new limiter.
func NewSlidingWindowLimiter() *SlidingWindowLimiter {
	return &SlidingWindowLimiter{
		windows: make(map[string][]time.Time),
	}
}

// Allow returns true if the request under keyID is within the rate limit.
// limit is the maximum number of requests allowed per minute.
func (l *SlidingWindowLimiter) Allow(keyID string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	// Filter to only requests within the last minute
	var recent []time.Time
	for _, t := range l.windows[keyID] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= limit {
		l.windows[keyID] = recent
		return false
	}

	l.windows[keyID] = append(recent, now)
	return true
}
