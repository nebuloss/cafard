// Package ratelimit counts attempts per key in fixed windows: enough to make
// guessing a password or a link code slow, and nothing more.
//
// It has no lockout. A limit that locked an account after N failures would
// let anyone lock anyone out by failing N times; a rate is a cost to the
// guesser only. In memory, so a restart forgets: an attacker gains one
// window per restart, which is not worth a table.
package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	seen   map[string]*bucket
	swept  time.Time
}

type bucket struct {
	start time.Time
	n     int
}

// New allows max attempts per key in each window.
func New(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, seen: map[string]*bucket{}}
}

// Allow counts an attempt for key and reports whether it is within the limit,
// and if not, how long until it would be.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.swept) > l.window {
		for k, b := range l.seen {
			if now.Sub(b.start) >= l.window {
				delete(l.seen, k)
			}
		}
		l.swept = now
	}
	b := l.seen[key]
	if b == nil || now.Sub(b.start) >= l.window {
		b = &bucket{start: now}
		l.seen[key] = b
	}
	b.n++
	if b.n > l.max {
		return false, b.start.Add(l.window).Sub(now)
	}
	return true, 0
}
