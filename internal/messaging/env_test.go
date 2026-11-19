package messaging

import (
	"context"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/writer"
	"github.com/nebuloss/cafard/internal/writer/writertest"
)

var t0 = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

type env struct {
	t      *testing.T
	stack  *writertest.Stack
	ctx    context.Context
	dir    string
	st     *store.Store
	w      *writer.W
	hub    *hub.Memory
	clk    *clock.Fake
	tokens *auth.Tokens
	svc    *Service
	jan    *store.Janitor
	d      deps.Deps
}

func defaultOptions() Options {
	o := Options{}
	o.Config.SetDefaults()
	o.Images.SetDefaults()
	return o
}

func newEnv(t *testing.T, tweak ...func(*Options)) *env {
	t.Helper()
	clk := clock.NewFake(t0)
	s := writertest.New(t, writertest.Options{Clock: clk, Writer: writer.Options{BatchEvery: time.Millisecond}})
	dir, st, h, stat, w, log := s.Dir, s.Store, s.Hub, s.Status, s.Writer, s.Log
	tokens := auth.NewTokens(st.DB(), clk)
	var ret store.Retention
	ret.SetDefaults()
	jan := store.NewJanitor(st, w.Exec, ret, clk, log)
	d := deps.Deps{Server: deps.Server{DataDir: dir}, Store: st, Writer: w, Hub: h, Status: stat, Tokens: tokens, Janitor: jan,
		Projects: s.Projects, Clock: clk, Log: log}
	o := defaultOptions()
	for _, f := range tweak {
		f(&o)
	}
	svc, err := New(context.Background(), d, o)
	if err != nil {
		t.Fatal(err)
	}
	d.Messaging, d.Blobs = svc, svc.Blobs()
	for _, task := range svc.JanitorTasks() {
		jan.Add(task)
	}
	return &env{t: t, stack: s, ctx: context.Background(), dir: dir, st: st, w: w, hub: h, clk: clk, tokens: tokens, svc: svc, jan: jan, d: d}
}

// principal is a member of the whole project lab with scopes (read and
// message when none), as most tests need.
func (e *env) principal(name, kind string, scopes ...auth.Scope) auth.Principal {
	e.t.Helper()
	return e.stack.Member(e.t, name, kind, scopes...)
}

// admin is the admin session, who reaches every project.
func (e *env) admin() auth.Principal {
	return auth.Principal{Name: auth.AdminName, Kind: auth.KindHuman, Scopes: auth.AdminScopes}
}

func (e *env) agent(name string) auth.Principal { return e.principal(name, auth.KindAgent) }
func (e *env) human(name string) auth.Principal { return e.principal(name, auth.KindHuman) }

func (e *env) line(stream, text string, sev int) int64 {
	e.t.Helper()
	seq, err := e.w.Commit(e.ctx, store.Entry{Stream: stream, Text: text, Severity: sev})
	if err != nil {
		e.t.Fatal(err)
	}
	return seq
}

func (e *env) mark(stream, text string) int64 {
	e.t.Helper()
	seq, err := e.w.Commit(e.ctx, store.Entry{Stream: stream, Kind: store.KindMarker, Text: text, Severity: store.DefaultSeverity})
	if err != nil {
		e.t.Fatal(err)
	}
	return seq
}

func (e *env) send(p auth.Principal, in SendInput) store.Entry {
	e.t.Helper()
	m, err := e.svc.SendMessage(e.ctx, p, in)
	if err != nil {
		e.t.Fatalf("send as %s: %v", p.Name, err)
	}
	return m
}

func text(s string) SendInput { return SendInput{SendRequest: sendReq(s, "", "")} }

func call(p auth.Principal) *mcpserver.Call { return &mcpserver.Call{Principal: p} }

func seqsOf(es []store.Entry) []int64 {
	out := make([]int64, len(es))
	for i, e := range es {
		out[i] = e.Seq
	}
	return out
}

// hookHub runs onSubscribe right after a subscription exists, the moment
// a message must not fall between the store read and the hub.
type hookHub struct {
	inner       *hub.Memory
	onSubscribe func()
}

func (h *hookHub) Subscribe(f hub.Filter, buffer int) hub.Subscription {
	sub := h.inner.Subscribe(f, buffer)
	if h.onSubscribe != nil {
		h.onSubscribe()
	}
	return sub
}

func hubFilterFor(p auth.Principal) hub.Filter {
	return hub.Filter{Streams: []string{"chan:lab/*", "dm:*"}, Kinds: []store.Kind{store.KindMessage}, MinSeverity: 7, Viewer: &p}
}
