package view

import (
	"sync"
	"time"
)

// OnlineWithin is how recent a principal's last request must be for the
// members panel to call it online when it holds no live feed.
const OnlineWithin = 5 * time.Minute

// Presence counts the open live feeds (GET /api/events) per principal:
// a page left open shows its person online although it sends nothing.
// The zero value is not usable; nil reports nobody.
type Presence struct {
	mu sync.Mutex
	n  map[string]int
}

// NewPresence returns an empty Presence.
func NewPresence() *Presence { return &Presence{n: map[string]int{}} }

// Enter counts one feed of name; the returned func ends it.
func (p *Presence) Enter(name string) func() {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	p.n[name]++
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			if p.n[name]--; p.n[name] <= 0 {
				delete(p.n, name)
			}
			p.mu.Unlock()
		})
	}
}

// Live reports whether name holds a feed open now.
func (p *Presence) Live(name string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n[name] > 0
}

// Online is Live, or a request within OnlineWithin of now.
func (p *Presence) Online(name string, lastSeen *time.Time, now time.Time) bool {
	return p.Live(name) || (lastSeen != nil && now.Sub(*lastSeen) < OnlineWithin)
}
