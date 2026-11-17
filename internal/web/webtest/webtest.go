// Package webtest builds a complete web stack over a temporary database,
// for the tests of internal/web and its subpackages.
package webtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/nebuloss/cafard/internal/writer/writertest"
)

// Now is the fake clock's start.
var Now = time.Date(2026, 11, 16, 9, 40, 0, 0, time.UTC)

// Env is a running stack. Mount adds the public handlers to Mux and the
// admin-only ones to AdminMux, which falls back to Mux as the admin
// interface does in cmd/cafard.
type Env struct {
	T        testing.TB
	Store    *store.Store
	Tokens   *auth.Tokens
	Sessions *auth.Sessions
	Account  *auth.Admin
	Hub      *hub.Memory
	Status   *status.Memory
	Writer   *writer.W
	Clock    *clock.Fake
	Deps     deps.Deps
	Mux      *http.ServeMux
	AdminMux *http.ServeMux
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
	clk := clock.NewFake(Now)
	s := writertest.New(t, writertest.Options{Clock: clk, Visible: o.Visible, Writer: writer.Options{BatchEvery: time.Millisecond}})
	st, h, stat, w, log := s.Store, s.Hub, s.Status, s.Writer, s.Log
	tokens := auth.NewTokens(st.DB(), clk)
	sessions := auth.NewSessions(st.DB(), clk)
	account := auth.NewAdmin(st.DB(), clk)
	e := &Env{T: t, Store: st, Tokens: tokens, Sessions: sessions, Account: account, Hub: h, Status: stat, Writer: w, Clock: clk,
		Mux: http.NewServeMux(), AdminMux: http.NewServeMux()}
	e.AdminMux.Handle("/", e.Mux)
	e.Deps = deps.Deps{
		Server:    deps.Server{PublicURL: "https://cafard.lan", HTTPListen: "127.0.0.1:8790", Secure: o.Secure},
		Store:     st,
		Writer:    w,
		Hub:       h,
		Status:    stat,
		Auth:      &auth.TokenAuthenticator{Tokens: tokens, Sessions: sessions.Lookup, Admin: account},
		Tokens:    tokens,
		Admin:     account,
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

// Admin is the admin's session cookie, as after signing in with the
// password on the admin interface. It skips the password's key
// derivation, which would cost most of a second per test.
func (e *Env) Admin() *http.Cookie {
	e.T.Helper()
	if _, err := e.Tokens.EnsurePrincipal(context.Background(), auth.AdminName, auth.KindHuman); err != nil {
		e.T.Fatal(err)
	}
	secret, err := e.Account.NewSession()
	if err != nil {
		e.T.Fatal(err)
	}
	return &http.Cookie{Name: auth.AdminCookie, Value: secret}
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

// Do sends a request to the admin interface, which serves every route, as
// one listener does when admin_listen is not set. cookie is optional; body
// is a form when it is url.Values, a multipart form when it is Multipart,
// raw JSON otherwise.
func (e *Env) Do(method, target string, cookie *http.Cookie, body any, header ...string) *httptest.ResponseRecorder {
	e.T.Helper()
	return e.serve(auth.AdminInterface(e.AdminMux), method, target, cookie, body, header...)
}

// DoPublic is Do on the public interface alone, as when admin_listen is
// set: no admin route, and the admin cookie counts for nothing.
func (e *Env) DoPublic(method, target string, cookie *http.Cookie, body any, header ...string) *httptest.ResponseRecorder {
	e.T.Helper()
	return e.serve(e.Mux, method, target, cookie, body, header...)
}

func (e *Env) serve(h http.Handler, method, target string, cookie *http.Cookie, body any, header ...string) *httptest.ResponseRecorder {
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
	h.ServeHTTP(rec, r)
	return rec
}
