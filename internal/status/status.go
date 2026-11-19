// Package status keeps the live state of every source in memory:
// connected, idle or down, line rate, drops and serial line state.
package status

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
)

// State is a source's state.
type State = dto.StreamState

// LineState is a serial port's modem lines.
type LineState = dto.LineState

// Source describes a stream and how it arrives.
type Source struct {
	Stream    string
	Transport string // "local" | "rfc2217" | "tcp" | "udp" | "http" | "otlp" | "push" | "internal"
}

// Registry is the status interface the rest of the server uses.
type Registry interface {
	Declare(s Source)
	Seen(stream string, ts time.Time)
	SetConnected(stream string, up bool, reason string)
	Dropped(stream string, n int)
	// Unrouted counts n entries of a transport that reached no sink and
	// were not stored.
	Unrouted(transport string, n int)
	UnroutedCounts() map[string]int64
	SetLineState(stream string, ls LineState)
	Snapshot() []dto.Stream
	Watch(buffer int) (<-chan dto.Stream, func())
}

const (
	// IdleAfter is how long a source may stay silent and still count as
	// connected.
	IdleAfter = 5 * time.Minute
	// notifyEvery throttles Watch notifications per stream.
	notifyEvery = 2 * time.Second

	bucketWidth = 10 * time.Second
	bucketCount = 30 // 30 x 10 s = the 5 minute rate window
)

type source struct {
	src         Source
	connectable bool
	up          bool
	reason      string
	lastSeen    time.Time
	counts      [bucketCount]int64
	slots       [bucketCount]int64 // which 10 s slot each bucket holds
	dropped     int64
	lineState   *LineState

	lastState    State
	lastNotified time.Time
	pending      bool
}

// Memory is the registry.
type Memory struct {
	mu       sync.Mutex
	clock    clock.Clock
	sources  map[string]*source
	watchers map[chan dto.Stream]struct{}
	unrouted map[string]int64
}

var _ Registry = (*Memory)(nil)

// New returns an empty registry.
func New(c clock.Clock) *Memory {
	return &Memory{clock: c, sources: map[string]*source{}, watchers: map[chan dto.Stream]struct{}{}, unrouted: map[string]int64{}}
}

func inferTransport(stream string) string {
	switch store.StreamKind(stream) {
	case "push":
		return "push"
	case "otlp":
		return "otlp"
	case "syslog", "netconsole":
		return "udp"
	}
	return "internal"
}

func (m *Memory) get(stream string) *source {
	s, ok := m.sources[stream]
	if !ok {
		s = &source{src: Source{Stream: stream, Transport: inferTransport(stream)}}
		m.sources[stream] = s
		s.lastState = m.state(s, m.clock.Now())
	}
	return s
}

// Declare registers a source before it sends anything, so it shows up idle.
func (m *Memory) Declare(src Source) {
	if store.IsMessageStream(src.Stream) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.get(src.Stream)
	if src.Transport != "" {
		s.src.Transport = src.Transport
	}
	m.changed(s)
}

// Seen counts one entry. The writer calls it for every committed entry.
func (m *Memory) Seen(stream string, _ time.Time) {
	if store.IsMessageStream(stream) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	s := m.get(stream)
	s.lastSeen = now
	slot := now.UnixNano() / int64(bucketWidth)
	i := slot % bucketCount
	if s.slots[i] != slot {
		s.slots[i], s.counts[i] = slot, 0
	}
	s.counts[i]++
	// Every entry is a change (rate, last activity, last line), so watchers
	// hear about an active source at the throttle's pace, not only when
	// its state flips: the Sources page stays live (track G).
	m.changed(s)
}

// SetConnected records a serial port or TCP peer coming up or going down.
func (m *Memory) SetConnected(stream string, up bool, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.get(stream)
	s.connectable, s.up, s.reason = true, up, reason
	m.changed(s)
}

// Dropped counts entries lost to back-pressure.
func (m *Memory) Dropped(stream string, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.get(stream)
	s.dropped += int64(n)
	m.changed(s)
}

// Unrouted counts entries that reached no sink, by transport.
func (m *Memory) Unrouted(transport string, n int) {
	m.mu.Lock()
	m.unrouted[transport] += int64(n)
	m.mu.Unlock()
}

// UnroutedCounts are the Unrouted totals since start.
func (m *Memory) UnroutedCounts() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.unrouted)
}

// SetLineState records the modem lines of a serial port.
func (m *Memory) SetLineState(stream string, ls LineState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.get(stream)
	s.lineState = &ls
	m.changed(s)
}

func (m *Memory) state(s *source, now time.Time) State {
	switch {
	case s.connectable && !s.up:
		return dto.StreamDown
	case s.lastSeen.IsZero() || now.Sub(s.lastSeen) > IdleAfter:
		return dto.StreamIdle
	}
	return dto.StreamConnected
}

func (m *Memory) rate(s *source, now time.Time) float64 {
	current := now.UnixNano() / int64(bucketWidth)
	var total int64
	for i := range bucketCount {
		if current-s.slots[i] < bucketCount {
			total += s.counts[i]
		}
	}
	return float64(total) / (bucketCount * bucketWidth).Minutes()
}

func (m *Memory) view(s *source, now time.Time) dto.Stream {
	v := dto.Stream{
		ID:         s.src.Stream,
		Kind:       store.StreamKind(s.src.Stream),
		Project:    store.ProjectOf(s.src.Stream),
		Sink:       store.LocalName(s.src.Stream),
		Transport:  s.src.Transport,
		Status:     m.state(s, now),
		RatePerMin: m.rate(s, now),
		Dropped:    s.dropped,
		LineState:  s.lineState,
	}
	if !s.lastSeen.IsZero() {
		t := dto.TimeOf(s.lastSeen)
		v.LastActivity = &t
	}
	return v
}

// changed notifies watchers, throttled per stream.
func (m *Memory) changed(s *source) {
	now := m.clock.Now()
	s.lastState = m.state(s, now)
	if now.Sub(s.lastNotified) < notifyEvery {
		s.pending = true
		return
	}
	m.notify(s, now)
}

func (m *Memory) notify(s *source, now time.Time) {
	s.lastNotified, s.pending = now, false
	v := m.view(s, now)
	for ch := range m.watchers {
		select {
		case ch <- v:
		default:
		}
	}
}

// Get returns one source's status.
func (m *Memory) Get(stream string) (dto.Stream, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sources[stream]
	if !ok {
		return dto.Stream{}, false
	}
	return m.view(s, m.clock.Now()), true
}

// Snapshot lists every source by id. last_seq, labels, last_line and claim
// are left for the caller, which has the store.
func (m *Memory) Snapshot() []dto.Stream {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	out := make([]dto.Stream, 0, len(m.sources))
	for _, s := range m.sources {
		out = append(out, m.view(s, now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Watch delivers state changes until cancel is called. A slow watcher
// misses updates rather than blocking anyone.
func (m *Memory) Watch(buffer int) (<-chan dto.Stream, func()) {
	if buffer < 1 {
		buffer = 1
	}
	ch := make(chan dto.Stream, buffer)
	m.mu.Lock()
	m.watchers[ch] = struct{}{}
	m.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.watchers, ch)
			m.mu.Unlock()
		})
	}
}

// Tick notices sources that went idle and flushes throttled notifications.
func (m *Memory) Tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	for _, s := range m.sources {
		if st := m.state(s, now); st != s.lastState {
			s.lastState = st
			s.pending = true
		}
		if s.pending && now.Sub(s.lastNotified) >= notifyEvery {
			m.notify(s, now)
		}
	}
}

// Run calls Tick every second until ctx ends.
func (m *Memory) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-m.clock.After(time.Second):
			m.Tick()
		}
	}
}

// Streams merges the store's streams with the live status, message
// streams left out, sorted by id. Claims are the caller's.
func Streams(ctx context.Context, st *store.Store, reg Registry) ([]dto.Stream, error) {
	rows, err := st.Streams(ctx)
	if err != nil {
		return nil, err
	}
	live := map[string]dto.Stream{}
	if reg != nil {
		for _, s := range reg.Snapshot() {
			live[s.ID] = s
		}
	}
	out := make([]dto.Stream, 0, len(rows)+len(live))
	seen := map[string]bool{}
	for _, r := range rows {
		if store.IsMessageStream(r.ID) {
			continue
		}
		seen[r.ID] = true
		s, ok := live[r.ID]
		if !ok {
			s = dto.Stream{ID: r.ID, Transport: "internal", Status: dto.StreamIdle, Project: store.ProjectOf(r.ID), Sink: store.LocalName(r.ID)}
			if r.LastTS != nil {
				s.LastActivity = dto.TimePtr(*r.LastTS)
			}
		}
		s.Kind, s.LastSeq, s.Labels, s.LastLine = r.Kind, r.LastSeq, r.Labels, textclean.StripANSI(r.LastLine)
		out = append(out, s)
	}
	for id, s := range live {
		if !seen[id] && !store.IsMessageStream(id) {
			if s.Kind == "" {
				s.Kind = store.StreamKind(id)
			}
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b dto.Stream) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
