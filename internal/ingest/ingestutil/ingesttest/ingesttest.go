// Package ingesttest has fakes of the writer and the status registry for
// the network ingesters' tests.
package ingesttest

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/writer"
)

// Writer records entries. While Full is set, a lossy Submit returns
// ErrFull and a blocking one waits for its context (or for Full to clear).
type Writer struct {
	mu      sync.Mutex
	entries []store.Entry
	full    bool
	// Limit, when > 0, makes the queue full once that many entries are in.
	Limit int
	// Refuse, when set, refuses entries the way the writer refuses bad input.
	Refuse func(store.Entry) error
	added  chan struct{}
}

// NewWriter returns an empty writer.
func NewWriter() *Writer { return &Writer{added: make(chan struct{}, 1)} }

// SetFull fills or empties the queue.
func (w *Writer) SetFull(full bool) {
	w.mu.Lock()
	w.full = full
	w.mu.Unlock()
}

func (w *Writer) isFull() bool {
	return w.full || (w.Limit > 0 && len(w.entries) >= w.Limit)
}

func (w *Writer) Submit(ctx context.Context, e store.Entry, lossy bool) error {
	if w.Refuse != nil {
		if err := w.Refuse(e); err != nil {
			return err
		}
	}
	for {
		w.mu.Lock()
		if !w.isFull() {
			w.entries = append(w.entries, e)
			w.mu.Unlock()
			select {
			case w.added <- struct{}{}:
			default:
			}
			return nil
		}
		w.mu.Unlock()
		if lossy {
			return writer.ErrFull
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (w *Writer) Commit(ctx context.Context, e store.Entry) (int64, error) {
	return 0, w.Submit(ctx, e, false)
}

func (w *Writer) CommitBatch(ctx context.Context, es []store.Entry) (int64, error) {
	for _, e := range es {
		if err := w.Submit(ctx, e, false); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

// Entries is a copy of what was queued.
func (w *Writer) Entries() []store.Entry {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]store.Entry(nil), w.entries...)
}

// Wait waits until at least n entries are in, or the timeout passes.
func (w *Writer) Wait(n int, timeout time.Duration) []store.Entry {
	deadline := time.After(timeout)
	for {
		if es := w.Entries(); len(es) >= n {
			return es
		}
		select {
		case <-w.added:
		case <-deadline:
			return w.Entries()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Status records what ingesters tell the registry.
type Status struct {
	mu        sync.Mutex
	Declared  map[string]string // stream to transport
	Drops     map[string]int
	Unrouteds map[string]int // by transport
	Connected map[string]bool
	Reasons   map[string]string
}

var _ status.Registry = (*Status)(nil)

// NewStatus returns an empty registry.
func NewStatus() *Status {
	return &Status{Declared: map[string]string{}, Drops: map[string]int{}, Unrouteds: map[string]int{}, Connected: map[string]bool{}, Reasons: map[string]string{}}
}

func (s *Status) Declare(src status.Source) {
	s.mu.Lock()
	s.Declared[src.Stream] = src.Transport
	s.mu.Unlock()
}
func (s *Status) Seen(string, time.Time) {}
func (s *Status) SetConnected(stream string, up bool, reason string) {
	s.mu.Lock()
	s.Connected[stream] = up
	s.Reasons[stream] = reason
	s.mu.Unlock()
}
func (s *Status) Dropped(stream string, n int) {
	s.mu.Lock()
	s.Drops[stream] += n
	s.mu.Unlock()
}
func (s *Status) Unrouted(transport string, n int) {
	s.mu.Lock()
	s.Unrouteds[transport] += n
	s.mu.Unlock()
}
func (s *Status) UnroutedCounts() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int64{}
	for k, v := range s.Unrouteds {
		out[k] = int64(v)
	}
	return out
}
func (s *Status) SetLineState(string, status.LineState) {}
func (s *Status) Snapshot() []dto.Stream                { return nil }
func (s *Status) Watch(int) (<-chan dto.Stream, func()) {
	return make(chan dto.Stream), func() {}
}

// Get reads the registry under its lock.
func (s *Status) Get(f func(s *Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// Deps returns deps with the fakes, a system clock and a silent log.
func Deps(w *Writer, st *Status) deps.Deps {
	return deps.Deps{Writer: w, Status: st, Clock: clock.System{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// Sinks is a projects service over a store of its own.
type Sinks struct {
	*projects.Service
	store *store.Store
}

// NewSinks holds sinks given as "project/sink", or
// "project/sink=10.0.0.0/8,10.1.2.3" with source addresses; their
// projects are made too.
func NewSinks(t testing.TB, sinks ...string) Sinks {
	t.Helper()
	ctx := context.Background()
	// Not t.TempDir: a fuzz seed's name has a '#', which a database path may not.
	dir, err := os.MkdirTemp("", "cafard-sinks")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(); os.RemoveAll(dir) })
	pr, err := projects.New(ctx, st.DB(), clock.System{}, store.Limits{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range sinks {
		ref, sources, _ := strings.Cut(spec, "=")
		project, name, ok := projects.SplitRef(ref)
		if !ok {
			t.Fatalf("sink %q is not project/sink", spec)
		}
		if _, err := pr.Ensure(ctx, project, ""); err != nil {
			t.Fatal(err)
		}
		k := projects.Sink{Project: project, Name: name}
		if k.Sources, err = projects.ParseSources(sources); err != nil {
			t.Fatal(err)
		}
		if _, err := pr.SaveSink(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	return Sinks{Service: pr, store: st}
}

// Member is an agent granted ingest on the whole of project, holding a
// token with that scope.
func (s Sinks) Member(t testing.TB, name, project string) auth.Principal {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Ensure(ctx, project, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.NewTokens(s.store.DB(), clock.System{}).EnsurePrincipal(ctx, name, auth.KindAgent); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGrants(ctx, projects.Grant{Principal: name, Project: project, Scopes: auth.NewScopeSet(auth.ScopeIngest)}); err != nil {
		t.Fatal(err)
	}
	return auth.Principal{Name: name, Kind: auth.KindAgent, Scopes: auth.NewScopeSet(auth.ScopeIngest), TokenID: 1}
}
