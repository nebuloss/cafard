// Package webtest builds a complete web stack over a temporary database,
// for the tests of internal/web and its subpackages.
package webtest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/writer"
)

// Now is the fake clock's start.
var Now = time.Date(2026, 11, 16, 9, 40, 0, 0, time.UTC)

// Env is a running stack. Mount adds handlers to Mux.
type Env struct {
	T        testing.TB
	Store    *store.Store
	Tokens   *auth.Tokens
	Sessions *auth.Sessions
	Hub      *hub.Memory
	Status   *status.Memory
	Writer   *writer.W
	Clock    *clock.Fake
	Deps     deps.Deps
	Mux      *http.ServeMux
}

// Options change the stack.
type Options struct {
	Visible   hub.VisibleFunc // the hub's message visibility; nil hides messages
	Messaging deps.Messaging
	Secure    bool
}

// New starts the stack; it stops at the end of the test.
func New(t testing.TB, o Options) *Env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(Now)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hub.New(o.Visible)
	stat := status.New(clk)
	w := writer.New(st, h, stat, clk, log, writer.Options{BatchEvery: time.Millisecond})
	done := make(chan struct{})
	go func() { _ = w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; st.Close() })
	tokens := auth.NewTokens(st.DB(), clk)
	sessions := auth.NewSessions(st.DB(), clk)
	e := &Env{T: t, Store: st, Tokens: tokens, Sessions: sessions, Hub: h, Status: stat, Writer: w, Clock: clk, Mux: http.NewServeMux()}
	e.Deps = deps.Deps{
		Server:    deps.Server{PublicURL: "https://cafard.lan", HTTPListen: "127.0.0.1:8790", Secure: o.Secure},
		Store:     st,
		Writer:    w,
		Hub:       h,
		Status:    stat,
		Auth:      &auth.TokenAuthenticator{Tokens: tokens, Sessions: sessions.Lookup},
		Tokens:    tokens,
		Janitor:   store.NewJanitor(st, w.Exec, store.Retention{}, clk, log),
		Messaging: o.Messaging,
		Clock:     clk,
		Log:       log,
		Version:   "test",
	}
	return e
}

// Principal makes a principal and a token for it.
func (e *Env) Principal(name, kind string, scopes ...auth.Scope) string {
	e.T.Helper()
	ctx := context.Background()
	if _, err := e.Tokens.EnsurePrincipal(ctx, name, kind); err != nil {
		e.T.Fatal(err)
	}
	_, secret, err := e.Tokens.CreateToken(ctx, name, "test", auth.NewScopeSet(scopes...))
	if err != nil {
		e.T.Fatal(err)
	}
	return secret
}

// Session opens a session for a principal and returns its cookie.
func (e *Env) Session(name string, scopes ...auth.Scope) *http.Cookie {
	e.T.Helper()
	_, secret, err := e.Sessions.Create(context.Background(), name, auth.NewScopeSet(scopes...), "Firefox on Linux", "Mozilla/5.0 Firefox/140.0")
	if err != nil {
		e.T.Fatal(err)
	}
	return &http.Cookie{Name: auth.SessionCookie, Value: secret}
}

// Admin is the human admin with a session, as after signing in.
func (e *Env) Admin() *http.Cookie {
	e.T.Helper()
	e.Principal("admin", auth.KindHuman, auth.ScopeIngest, auth.ScopeRead, auth.ScopeMessage, auth.ScopeAdmin)
	return e.Session("admin", auth.ScopeIngest, auth.ScopeRead, auth.ScopeMessage, auth.ScopeAdmin)
}

// Commit writes entries through the writer and returns the last seq.
func (e *Env) Commit(es ...store.Entry) int64 {
	e.T.Helper()
	for i := range es {
		if es[i].TS.IsZero() {
			es[i].TS = Now
		}
	}
	seq, err := e.Writer.CommitBatch(context.Background(), es)
	if err != nil {
		e.T.Fatal(err)
	}
	return seq
}

// Line is a log line with the default severity.
func Line(stream, text string) store.Entry {
	return store.Entry{Stream: stream, Kind: store.KindLine, Severity: store.DefaultSeverity, Text: text}
}

// Request is a request with optional cookie; body is a form when it is
// url.Values, a multipart form when it is Multipart, raw JSON otherwise.
func (e *Env) Do(method, target string, cookie *http.Cookie, body any, header ...string) *httptest.ResponseRecorder {
	e.T.Helper()
	var r *http.Request
	switch b := body.(type) {
	case nil:
		r = httptest.NewRequest(method, target, nil)
	case url.Values:
		r = httptest.NewRequest(method, target, strings.NewReader(b.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	case string:
		r = httptest.NewRequest(method, target, strings.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	case Multipart:
		ctype, buf := b.encode()
		r = httptest.NewRequest(method, target, buf)
		r.Header.Set("Content-Type", ctype)
	default:
		e.T.Fatalf("body type %T", body)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	e.Mux.ServeHTTP(rec, r)
	return rec
}
