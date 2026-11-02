// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"sync"
	"time"
)

// Limiter slows down password and setup-code guessing per key (client IP).
// The first FreeAttempts failures are free; after that each failure locks the
// key for an exponentially growing delay, capped at MaxLock. A success resets.
type Limiter struct {
	FreeAttempts int
	BaseLock     time.Duration
	MaxLock      time.Duration
	Now          func() time.Time

	mu      sync.Mutex
	entries map[string]*limitEntry
}

type limitEntry struct {
	failures    int
	lockedUntil time.Time
	lastFailure time.Time
}

// NewLimiter returns the default policy: 5 free attempts, then 30 s doubling up to 15 min.
func NewLimiter() *Limiter {
	return &Limiter{FreeAttempts: 5, BaseLock: 30 * time.Second, MaxLock: 15 * time.Minute, Now: time.Now}
}

// Allow reports whether key may try now, and if not, how long to wait.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return true, 0
	}
	if wait := e.lockedUntil.Sub(l.Now()); wait > 0 {
		return false, wait
	}
	return true, 0
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string]*limitEntry{}
	}
	now := l.Now()
	l.gc(now)
	e := l.entries[key]
	if e == nil {
		e = &limitEntry{}
		l.entries[key] = e
	}
	e.failures++
	e.lastFailure = now
	if over := e.failures - l.FreeAttempts; over > 0 {
		lock := l.BaseLock << min(over-1, 20)
		if lock > l.MaxLock || lock <= 0 {
			lock = l.MaxLock
		}
		e.lockedUntil = now.Add(lock)
	}
}

// Success clears the key.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// gc drops entries idle for longer than a day, bounding memory.
func (l *Limiter) gc(now time.Time) {
	if len(l.entries) < 1024 {
		return
	}
	for k, e := range l.entries {
		if now.Sub(e.lastFailure) > 24*time.Hour && now.After(e.lockedUntil) {
			delete(l.entries, k)
		}
	}
}
