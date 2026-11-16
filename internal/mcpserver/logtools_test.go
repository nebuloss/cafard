package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"math/rand/v2"
	"path/filepath"
	"strconv"
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

// hookHub runs onSubscribe right after a subscription exists, which is the
// moment an entry must not fall between the store read and the hub.
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

func newTestServer(t *testing.T) (*Server, *writer.W, *hookHub) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mem := hub.New(nil)
	hh := &hookHub{inner: mem}
	w := writer.New(st, mem, status.New(clock.System{}), clock.System{}, log, writer.Options{BatchEvery: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		st.Close()
	})
	d := deps.Deps{Store: st, Writer: w, Hub: hh, Clock: clock.System{}, Log: log}
	return New(d, Config{}), w, hh
}

var agent = &Call{Principal: auth.Principal{Name: "a", Kind: auth.KindAgent, Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage)}}

func TestWaitForMissesNothingCommittedWhileSubscribing(t *testing.T) {
	s, w, hh := newTestServer(t)
	ctx := context.Background()
	if _, err := w.Commit(ctx, store.Entry{Stream: "serial:dut1", Text: "boot", Severity: 6}); err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		needle := "needle-" + strconv.Itoa(i)
		delay := time.Duration(rand.IntN(3000)) * time.Microsecond
		hh.onSubscribe = func() {
			go func() {
				time.Sleep(delay)
				if _, err := w.Commit(ctx, store.Entry{Stream: "serial:dut1", Text: needle, Severity: 6}); err != nil {
					t.Error(err)
				}
			}()
		}
		start := time.Now()
		text, out, err := s.waitFor(ctx, agent, waitIn{Pattern: "^" + needle + "$", TimeoutS: 5})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Matched {
			t.Fatalf("iteration %d (delay %s): the entry was lost: %s", i, delay, text)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatalf("iteration %d took %s", i, time.Since(start))
		}
	}
}

func TestWaitForCatchesUpFromSinceSeq(t *testing.T) {
	s, w, hh := newTestServer(t)
	ctx := context.Background()
	hh.onSubscribe = nil
	var first int64
	for i := range 2500 {
		seq, err := w.Commit(ctx, store.Entry{Stream: "push:a", Text: "line " + strconv.Itoa(i), Severity: 6})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = seq
		}
	}
	since := first
	_, out, err := s.waitFor(ctx, agent, waitIn{Pattern: "^line 2499$", SinceSeq: &since, TimeoutS: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Matched || out.MatchSeq != first+2499 {
		t.Fatalf("out = %+v", out)
	}
}

func TestWaitForReleasesItsSubscriptionOnCancel(t *testing.T) {
	s, _, hh := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, _, err := s.waitFor(ctx, agent, waitIn{Pattern: "never", TimeoutS: 60})
		errs <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for hh.inner.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-errs:
		if err == nil {
			t.Error("want the context error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait_for ignored the cancel")
	}
	if n := hh.inner.Subscribers(); n != 0 {
		t.Errorf("%d subscriptions left after cancel", n)
	}
}

func TestLogToolsNeverReturnMessages(t *testing.T) {
	s, w, _ := newTestServer(t)
	ctx := context.Background()
	if _, err := w.CommitBatch(ctx, []store.Entry{
		{Stream: "dm:a~b", Kind: store.KindMessage, Text: "secret", Sender: "b", Severity: 6},
		{Stream: "chan:general", Kind: store.KindMessage, Text: "hello all", Sender: "b", Severity: 6},
		{Stream: "push:a", Text: "log line", Severity: 6},
	}); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	for _, streams := range [][]string{nil, {"*"}, {"dm:*", "chan:*"}, {"dm:a~b"}} {
		text, _, err := s.read(ctx, agent, readIn{selectIn: selectIn{Streams: streams}, SinceSeq: &zero})
		if err != nil {
			t.Fatal(err)
		}
		if contains(text, "secret") || contains(text, "hello all") {
			t.Errorf("read %v returned a message: %q", streams, text)
		}
	}
	text, _, _ := s.tail(ctx, agent, tailIn{selectIn: selectIn{Streams: []string{"dm:*"}}})
	if contains(text, "secret") {
		t.Errorf("tail returned a message: %q", text)
	}
	if _, _, err := s.context(ctx, agent, contextIn{Seq: 1}); err == nil {
		t.Error("context on a DM seq: want not_found without messaging")
	}
	streams, err := s.streams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range streams {
		if store.IsMessageStream(st.ID) {
			t.Errorf("list_streams shows %s", st.ID)
		}
	}
}

func TestUnknownChannelIsAnError(t *testing.T) {
	s, _, _ := newTestServer(t)
	_, _, err := s.read(context.Background(), agent, readIn{selectIn: selectIn{Channel: "dut1"}})
	if err == nil {
		t.Fatal("want unknown_channel")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
