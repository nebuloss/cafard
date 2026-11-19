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

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/projects"
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

// Project is the project a Stack makes, where Member grants.
const Project = "lab"

// Stack is a running writer over its store. It stops at the end of the test.
type Stack struct {
	Dir      string
	Store    *store.Store
	Hub      *hub.Memory
	Status   *status.Memory
	Writer   *writer.W
	Projects *projects.Service // with Project made
	Clock    clock.Clock
	Log      *slog.Logger

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
	if s.Projects, err = projects.New(context.Background(), st.DB(), o.Clock, store.Limits{}, s.Log); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Projects.Create(context.Background(), Project, "Lab"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() { s.done <- s.Writer.Run(ctx) }()
	t.Cleanup(func() {
		s.Stop()
		st.Close()
	})
	return s
}

// Member makes the principal name of kind if needed, grants it scopes
// (read and message when none) on the whole of Project, and returns it
// holding those scopes, as a token would.
func (s *Stack) Member(t testing.TB, name, kind string, scopes ...auth.Scope) auth.Principal {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []auth.Scope{auth.ScopeRead, auth.ScopeMessage}
	}
	if _, err := auth.NewTokens(s.Store.DB(), s.Clock).EnsurePrincipal(context.Background(), name, kind); err != nil {
		t.Fatal(err)
	}
	var granted []auth.Scope
	for _, sc := range scopes {
		if projects.GrantScopes.Has(sc) {
			granted = append(granted, sc)
		}
	}
	g := projects.Grant{Principal: name, Project: Project, Scopes: auth.NewScopeSet(granted...)}
	if err := s.Projects.AddGrants(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	return auth.Principal{Name: name, Kind: kind, Scopes: auth.NewScopeSet(scopes...), TokenID: 1}
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
