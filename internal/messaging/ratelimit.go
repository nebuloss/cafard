package messaging

import (
	"math"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
)

// limiter is a token bucket per key, on an injected clock.
type limiter struct {
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

func newLimiter(c clock.Clock, rate, burst float64) *limiter {
	return &limiter{clock: c, rate: rate, burst: burst, buckets: map[string]*bucket{}}
}

// take spends one token for key. When none is left it returns false and
// how long until one is.
func (l *limiter) take(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	b, ok := l.buckets[key]
	if !ok {
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

// retrySeconds rounds a wait up to whole seconds, at least 1.
func retrySeconds(d time.Duration) int {
	return max(1, int(math.Ceil(d.Seconds())))
}
