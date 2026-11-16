// Package hub fans committed entries out to live subscribers: wait_for,
// wait_message, ask_human and SSE. Only the writer publishes, after commit.
package hub

import (
	"regexp"
	"slices"
	"sync"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/store"
)

// Filter selects entries for one subscriber.
type Filter struct {
	Streams     []string     // exact ids or path.Match globs; empty = all non-message streams
	Kinds       []store.Kind // empty = all
	MinSeverity int          // keep entries with Severity <= MinSeverity; 7 = all
	Pattern     *regexp.Regexp
	Viewer      *auth.Principal // non-nil: message entries pass only if visible to Viewer
}

// Hub is what subscribers see.
type Hub interface {
	Subscribe(f Filter, buffer int) Subscription
}

// Subscription is one subscriber's feed.
type Subscription interface {
	C() <-chan store.Entry
	// Lagged is closed if the subscriber fell behind and was dropped. It
	// must then re-read from the store from its last seq.
	Lagged() <-chan struct{}
	Close()
}

// VisibleFunc decides whether a message entry is visible to a principal.
type VisibleFunc func(p auth.Principal, e store.Entry) bool

// Memory is the in-process hub.
type Memory struct {
	mu      sync.RWMutex
	subs    map[*sub]struct{}
	visible VisibleFunc
}

// New returns a hub. visible decides message visibility for filters with a
// Viewer; nil hides every message from such filters.
func New(visible VisibleFunc) *Memory {
	if visible == nil {
		visible = func(auth.Principal, store.Entry) bool { return false }
	}
	return &Memory{subs: map[*sub]struct{}{}, visible: visible}
}

// SetVisible replaces the visibility rule (messaging installs its own).
func (h *Memory) SetVisible(v VisibleFunc) {
	h.mu.Lock()
	h.visible = v
	h.mu.Unlock()
}

type sub struct {
	hub    *Memory
	filter Filter
	ch     chan store.Entry
	lagged chan struct{}
	once   sync.Once
}

func (s *sub) C() <-chan store.Entry   { return s.ch }
func (s *sub) Lagged() <-chan struct{} { return s.lagged }

func (s *sub) Close() {
	s.hub.mu.Lock()
	delete(s.hub.subs, s)
	s.hub.mu.Unlock()
}

// Subscribe registers a filter. buffer is the number of entries that may
// queue before the subscriber is dropped as lagged.
func (h *Memory) Subscribe(f Filter, buffer int) Subscription {
	if buffer < 1 {
		buffer = 1
	}
	s := &sub{hub: h, filter: f, ch: make(chan store.Entry, buffer), lagged: make(chan struct{})}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Publish delivers committed entries. It never blocks: a subscriber whose
// buffer is full is dropped and told so through Lagged.
func (h *Memory) Publish(entries []store.Entry) {
	var dropped []*sub
	h.mu.RLock()
	visible := h.visible
	for s := range h.subs {
	entries:
		for _, e := range entries {
			if !s.filter.Match(e, visible) {
				continue
			}
			select {
			case s.ch <- e:
			default:
				dropped = append(dropped, s)
				break entries
			}
		}
	}
	h.mu.RUnlock()
	if len(dropped) == 0 {
		return
	}
	h.mu.Lock()
	for _, s := range dropped {
		delete(h.subs, s)
		s.once.Do(func() { close(s.lagged) })
	}
	h.mu.Unlock()
}

// Subscribers is the number of live subscriptions.
func (h *Memory) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Match reports whether e passes f.
func (f Filter) Match(e store.Entry, visible VisibleFunc) bool {
	if !store.StreamMatches(e.Stream, f.Streams) {
		return false
	}
	if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, e.Kind) {
		return false
	}
	if e.Severity > f.MinSeverity {
		return false
	}
	if f.Pattern != nil && !f.Pattern.MatchString(e.Text) {
		return false
	}
	if f.Viewer != nil && store.IsMessageStream(e.Stream) && (visible == nil || !visible(*f.Viewer, e)) {
		return false
	}
	return true
}
