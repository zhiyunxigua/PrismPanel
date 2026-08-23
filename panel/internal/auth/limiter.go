package auth

import (
	"sync"
	"time"
)

type attempt struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
}

type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]attempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: make(map[string]attempt)}
}

func (l *loginLimiter) allowed(keys []string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		entry := l.entries[key]
		if now.Before(entry.blockedUntil) {
			return false
		}
	}
	return true
}

func (l *loginLimiter) failure(keys []string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		entry := l.entries[key]
		if entry.windowStart.IsZero() || now.Sub(entry.windowStart) > 15*time.Minute {
			entry = attempt{windowStart: now}
		}
		entry.failures++
		if entry.failures >= 5 {
			entry.blockedUntil = now.Add(15 * time.Minute)
		}
		l.entries[key] = entry
	}
}

func (l *loginLimiter) success(keys []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		delete(l.entries, key)
	}
}
