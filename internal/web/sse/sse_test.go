package sse_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/sse"
	"github.com/nebuloss/cafard/internal/web/view"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

// fakeMessaging: a DM is visible to its two parties only, channels to
// everyone; "dut1" is bound to serial:dut1.
type fakeMessaging struct{}

func (fakeMessaging) Send(context.Context, auth.Principal, dto.SendRequest) (dto.Entry, error) {
	return dto.Entry{}, errors.New("not in this test")
}

func (fakeMessaging) Visible(_ context.Context, p auth.Principal, e store.Entry) bool {
	if pair, ok := strings.CutPrefix(e.Stream, "dm:"); ok {
		a, b, _ := strings.Cut(pair, "~")
		return p.Name == a || p.Name == b
	}
	return true
}

func (fakeMessaging) ResolveChannel(_ context.Context, name string) ([]string, error) {
	if name == "dut1" {
		return []string{"serial:dut1"}, nil
	}
	return nil, errors.New("no such channel")
}

func (fakeMessaging) ChatOf(e store.Entry) string {
	if store.IsMessageStream(e.Stream) {
		return e.Stream
	}
	return ""
}

type client struct {
	events <-chan webtest.Event
	cancel func()
}

func connect(t *testing.T, srv *httptest.Server, path, token string, header ...string) (*client, *http.Response) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	c := &client{cancel: func() { cancel(); resp.Body.Close() }}
	t.Cleanup(c.cancel)
	if resp.StatusCode != 200 {
		return c, resp
	}
	c.events = webtest.ReadEvents(resp.Body)
	return c, resp
}

// next returns the next event that is not a heartbeat (unless want is "ping").
func (c *client) next(t *testing.T, want string) webtest.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				t.Fatalf("stream closed waiting for %s", want)
			}
			if ev.Comment && want != "ping" {
				continue
			}
			if want == "ping" && !ev.Comment {
				continue
			}
			if want != "" && want != "ping" && ev.Name != want {
				continue
			}
			return ev
		case <-timeout:
			t.Fatalf("no %s event", want)
		}
	}
}

// none checks that nothing but heartbeats and counts arrives for a while.
func (c *client) none(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				return
			}
			if ev.Name == "entry" || ev.Name == "reset" {
				t.Errorf("unexpected %s: %s", ev.Name, ev.Data)
			}
		case <-deadline:
			return
		}
	}
}

func newServer(t *testing.T, o webtest.Options, tune func(*sse.Handler)) (*webtest.Env, *httptest.Server) {
	t.Helper()
	e := webtest.New(t, o)
	h := sse.New(e.Deps, view.NewKinds(e.Tokens))
	h.Heartbeat = time.Hour
	h.CountsEvery = 10 * time.Millisecond
	if tune != nil {
		tune(h)
	}
	sse.Register(e.Mux, h)
	srv := httptest.NewServer(e.Mux)
	t.Cleanup(srv.Close)
	return e, srv
}

func entryOf(t *testing.T, ev webtest.Event) dto.Entry {
	t.Helper()
	var e dto.Entry
	if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestCountsFirstThenStreamStatus(t *testing.T) {
	e, srv := newServer(t, webtest.Options{}, nil)
	tok := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	c, resp := connect(t, srv, "/api/events?scope=status", tok)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content type %q", ct)
	}
	first := c.next(t, "")
	if first.Name != "counts" {
		t.Fatalf("first event %q", first.Name)
	}
	e.Status.SetConnected("serial:dut1", false, "unplugged")
	ev := c.next(t, "stream_status")
	var st dto.Stream
	_ = json.Unmarshal([]byte(ev.Data), &st)
	if st.ID != "serial:dut1" || st.Status != dto.StreamDown {
		t.Errorf("stream_status %+v", st)
	}
	var counts dto.Counts
	_ = json.Unmarshal([]byte(c.next(t, "counts").Data), &counts)
	if counts.SourcesDown != 1 {
		t.Errorf("counts after a source went down: %+v", counts)
	}
	e.Commit(webtest.Line("serial:dut1", "status scope carries no entries"))
	c.none(t, 100*time.Millisecond)
}

func TestLiveEntriesFollowTheFilter(t *testing.T) {
	e, srv := newServer(t, webtest.Options{}, nil)
	tok := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	c, _ := connect(t, srv, "/api/events?streams=serial:*&sev=warning", tok)
	c.next(t, "counts")
	e.Commit(webtest.Line("serial:dut1", "info, filtered out"))
	e.Commit(store.Entry{Stream: "syslog:x", Kind: store.KindLine, Severity: 3, Text: "other stream"})
	seq := e.Commit(store.Entry{Stream: "serial:dut1", Kind: store.KindLine, Severity: 3, Text: "\x1b[31merror\x1b[0m"})
	ev := c.next(t, "entry")
	got := entryOf(t, ev)
	if ev.ID != webtest.Itoa(seq) || got.Seq != seq || got.Text != "\x1b[31merror\x1b[0m" || got.Severity != 3 {
		t.Errorf("entry id=%s %+v", ev.ID, got)
	}
}

func TestReplayFromLastEventID(t *testing.T) {
	e, srv := newServer(t, webtest.Options{}, nil)
	tok := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	first := e.Commit(webtest.Line("serial:dut1", "one"))
	e.Commit(webtest.Line("serial:dut1", "two"), webtest.Line("serial:dut1", "three"))

	c, _ := connect(t, srv, "/api/events", tok, "Last-Event-ID", webtest.Itoa(first))
	c.next(t, "counts")
	if got := entryOf(t, c.next(t, "entry")); got.Text != "two" {
		t.Errorf("replay starts at %q", got.Text)
	}
	if got := entryOf(t, c.next(t, "entry")); got.Text != "three" {
		t.Errorf("replay continues with %q", got.Text)
	}
	e.Commit(webtest.Line("serial:dut1", "four"))
	if got := entryOf(t, c.next(t, "entry")); got.Text != "four" {
		t.Errorf("live after replay: %q", got.Text)
	}
	c.none(t, 50*time.Millisecond)

	// since_seq wins over Last-Event-ID and is inclusive.
	c2, _ := connect(t, srv, "/api/events?since_seq="+webtest.Itoa(first), tok, "Last-Event-ID", "999")
	if got := entryOf(t, c2.next(t, "entry")); got.Text != "one" {
		t.Errorf("since_seq replay starts at %q", got.Text)
	}
}

func TestATooLongReplayIsAReset(t *testing.T) {
	e, srv := newServer(t, webtest.Options{}, func(h *sse.Handler) { h.ReplayMax = 3 })
	tok := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	for range 5 {
		e.Commit(webtest.Line("serial:dut1", "x"))
	}
	c, _ := connect(t, srv, "/api/events?since_seq=1", tok)
	if ev := c.next(t, ""); ev.Name != "counts" {
		t.Fatalf("first %q", ev.Name)
	}
	ev := c.next(t, "")
	if ev.Name != "reset" {
		t.Fatalf("got %q, want reset", ev.Name)
	}
}

func TestALaggingSubscriberIsReset(t *testing.T) {
	e, srv := newServer(t, webtest.Options{}, func(h *sse.Handler) { h.Buffer = 1 })
	tok := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	c, _ := connect(t, srv, "/api/events", tok)
	c.next(t, "counts")
	// One batch of many entries overflows a buffer of one at once.
	var es []store.Entry
	for range 50 {
		es = append(es, webtest.Line("serial:dut1", "burst"))
	}
	e.Commit(es...)
	c.next(t, "reset")
}

func TestHeartbeat(t *testing.T) {
	e, srv := newServer(t, webtest.Options{}, func(h *sse.Handler) { h.Heartbeat = 20 * time.Millisecond })
	tok := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	c, _ := connect(t, srv, "/api/events?scope=status", tok)
	if ev := c.next(t, "ping"); ev.Data != "ping" {
		t.Errorf("heartbeat %q", ev.Data)
	}
}

func TestDisconnectUnsubscribes(t *testing.T) {
	e, srv := newServer(t, webtest.Options{}, nil)
	tok := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	c, _ := connect(t, srv, "/api/events", tok)
	c.next(t, "counts")
	if n := e.Hub.Subscribers(); n != 1 {
		t.Fatalf("%d subscribers", n)
	}
	c.cancel()
	deadline := time.Now().Add(5 * time.Second)
	for e.Hub.Subscribers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the subscription outlived the connection")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The hub here lets every message through, so only the handler's own
// check stands between a DM and a third party.
func TestADirectMessageNeverReachesANonParty(t *testing.T) {
	letAll := func(auth.Principal, store.Entry) bool { return true }
	e, srv := newServer(t, webtest.Options{Visible: letAll, Messaging: fakeMessaging{}}, nil)
	carol := e.Principal("carol", auth.KindHuman, auth.ScopeRead)
	alice := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	dm := store.Entry{Stream: "dm:alice~bob", Kind: store.KindMessage, Severity: 6, Text: "private", Sender: "bob", Recipient: "alice"}
	e.Commit(dm)

	for _, path := range []string{"/api/events?kinds=message,line&since_seq=1", "/api/events?scope=home&since_seq=1", "/api/events?chat=dm:bob&since_seq=1"} {
		c, _ := connect(t, srv, path, carol)
		c.next(t, "counts")
		e.Commit(dm)
		c.none(t, 100*time.Millisecond)
	}
	// Alice, a party, gets it, both replayed and live.
	c, _ := connect(t, srv, "/api/events?chat=dm:bob&since_seq=1", alice)
	if got := entryOf(t, c.next(t, "entry")); got.Text != "private" || got.Chat != "dm:alice~bob" {
		t.Errorf("alice got %+v", got)
	}
	// A channel chat carries the channel's messages and its bound streams.
	c, _ = connect(t, srv, "/api/events?chat=chan:dut1", carol)
	c.next(t, "counts")
	e.Commit(store.Entry{Stream: "chan:dut1", Kind: store.KindMessage, Severity: 6, Text: "hello", Sender: "codex"})
	e.Commit(webtest.Line("serial:dut1", "bound line"), webtest.Line("serial:other", "unbound"))
	if got := entryOf(t, c.next(t, "entry")); got.Text != "hello" {
		t.Errorf("channel message %q", got.Text)
	}
	if got := entryOf(t, c.next(t, "entry")); got.Text != "bound line" {
		t.Errorf("bound line %q", got.Text)
	}
	c.none(t, 50*time.Millisecond)
}

func TestWithoutMessagingNoMessageIsSent(t *testing.T) {
	letAll := func(auth.Principal, store.Entry) bool { return true }
	e, srv := newServer(t, webtest.Options{Visible: letAll}, nil)
	tok := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	msg := store.Entry{Stream: "chan:general", Kind: store.KindMessage, Severity: 6, Text: "hi", Sender: "bob"}
	e.Commit(msg)
	c, _ := connect(t, srv, "/api/events?kinds=message,line&since_seq=1", tok)
	c.next(t, "counts")
	e.Commit(msg)
	c.none(t, 100*time.Millisecond)
	if _, resp := connect(t, srv, "/api/events?chat=chan:general", tok); resp.StatusCode != http.StatusNotFound {
		t.Errorf("chat scope without messaging: %d", resp.StatusCode)
	}
	if _, resp := connect(t, srv, "/api/events", "cfd_nope"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad token: %d", resp.StatusCode)
	}
}
