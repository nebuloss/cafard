// Package writertest opens a store over a temporary database and runs a
// hub, a status registry and a writer on it, for tests.
package writertest

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/writer"
)

// Options change the stack. The zero value is a system clock, a hub that
// hides messages, and the writer's own defaults.
type Options struct {
	Dir     string          // where the database goes; default t.TempDir()
	Clock   clock.Clock     // default clock.System{}
	Visible hub.VisibleFunc // the hub's message visibility
	Seer    writer.Seer     // told about commits instead of Status
	Writer  writer.Options
}

// Stack is a running writer over its store. It stops at the end of the test.
type Stack struct {
	Dir    string
	Store  *store.Store
	Hub    *hub.Memory
	Status *status.Memory
	Writer *writer.W
	Clock  clock.Clock
	Log    *slog.Logger

	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
}

// New opens the store and starts the writer.
func New(t testing.TB, o Options) *Stack {
	t.Helper()
	if o.Dir == "" {
		o.Dir = t.TempDir()
	}
	if o.Clock == nil {
		o.Clock = clock.System{}
	}
	st, err := store.Open(context.Background(), filepath.Join(o.Dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	s := &Stack{Dir: o.Dir, Store: st, Hub: hub.New(o.Visible), Status: status.New(o.Clock), Clock: o.Clock,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), done: make(chan error, 1)}
	var seer writer.Seer = s.Status
	if o.Seer != nil {
		seer = o.Seer
	}
	s.Writer = writer.New(st, s.Hub, seer, o.Clock, s.Log, o.Writer)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() { s.done <- s.Writer.Run(ctx) }()
	t.Cleanup(func() {
		s.Stop()
		st.Close()
	})
	return s
}

// Stop stops the writer, after it commits what is queued, and returns what
// Run returned. Calling it again returns the same.
func (s *Stack) Stop() error {
	s.once.Do(func() {
		s.cancel()
		s.err = <-s.done
	})
	return s.err
}
