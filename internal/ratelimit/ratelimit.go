// Package ratelimit is a token bucket per key, on an injected clock.
package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
)

// pruneAbove bounds the buckets kept: keyed by a remote address, the map
// would otherwise grow with every address that ever knocked.
const pruneAbove = 4096

// Limiter is a token bucket per key.
type Limiter struct {
	clock clock.Clock
	rate  float64 // tokens per second
	burst float64

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New makes a limiter that refills rate tokens per second up to burst.
func New(c clock.Clock, rate, burst float64) *Limiter {
	return &Limiter{clock: c, rate: rate, burst: burst, buckets: map[string]*bucket{}}
}

// Take spends one token for key. When none is left it returns false and
// how long until one is.
func (l *Limiter) Take(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= pruneAbove {
			l.prune(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// prune drops the buckets that have refilled: forgetting one changes
// nothing, since a new bucket starts full.
func (l *Limiter) prune(now time.Time) {
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// RetrySeconds rounds a wait up to whole seconds, at least 1.
func RetrySeconds(d time.Duration) int {
	return max(1, int(math.Ceil(d.Seconds())))
}
