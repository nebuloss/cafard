package syslog

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/ingest/ingestutil/ingesttest"
	"github.com/nebuloss/cafard/internal/store"
)

var testIP = netip.MustParseAddr("192.0.2.7")

func TestFraming(t *testing.T) {
	frame := func(s string) string { return fmt.Sprintf("%d %s", len(s), s) }
	tests := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"octet counting", []string{frame("<13>1 - h a - - - one") + frame("<13>1 - h a - - - two")},
			[]string{"<13>1 - h a - - - one", "<13>1 - h a - - - two"}},
		{"octet counting byte by byte", strings.Split(frame("<13>x")+frame("a\nb"), ""),
			[]string{"<13>x", "a\nb"}},
		{"octet frames separated by newlines", []string{frame("<1>a") + "\n" + frame("<1>b") + "\r\n"},
			[]string{"<1>a", "<1>b"}},
		{"newline framing", []string{"<13>one\n<13>tw", "o\r\n<13>three\rfour\n"},
			[]string{"<13>one", "<13>two", "<13>three", "four"}},
		{"leading blank lines before newline framing", []string{"\n\n<13>x\n"}, []string{"<13>x"}},
		{"newline framing keeps the last partial line for Close", []string{"<13>a\n<13>b"}, []string{"<13>a", "<13>b"}},
		{"octet garbage falls back to newlines", []string{"12abc\n<13>d\n"}, []string{"12abc", "<13>d"}},
		{"zero length falls back", []string{"0 x\n"}, []string{"0 x"}},
		{"truncated frame is flushed on close", []string{"10 abc"}, []string{"abc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			f := newFramer(func(m []byte, _ time.Time) { got = append(got, string(m)) })
			for _, c := range tt.chunks {
				f.Write([]byte(c), now)
			}
			f.Close()
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFramingCapsLongFrames(t *testing.T) {
	var got []int
	f := newFramer(func(m []byte, _ time.Time) { got = append(got, len(m)) })
	n := MaxFrame + 100
	f.Write([]byte(fmt.Sprintf("%d %s", n, strings.Repeat("x", n))), now)
	f.Write([]byte("3 abc"), now)
	f.Close()
	if len(got) != 2 || got[0] != MaxFrame || got[1] != 3 {
		t.Fatalf("frames %v", got)
	}
}

func TestFramingFlushesAPartialLineAfterSilence(t *testing.T) {
	var got []string
	f := newFramer(func(m []byte, _ time.Time) { got = append(got, string(m)) })
	f.Write([]byte("<13>login: "), now)
	f.Tick(now.Add(100 * time.Millisecond))
	if len(got) != 0 {
		t.Fatalf("flushed too early: %q", got)
	}
	f.Tick(now.Add(time.Second))
	if len(got) != 1 || got[0] != "<13>login: " {
		t.Fatalf("got %q", got)
	}
}

func FuzzFraming(f *testing.F) {
	f.Add([]byte("5 <1>ab3 xyz"), 3)
	f.Add([]byte("<13>a\r\nb\rc\n"), 1)
	f.Add([]byte("99999999999 x"), 2)
	f.Fuzz(func(t *testing.T, b []byte, cut int) {
		total := 0
		fr := newFramer(func(m []byte, _ time.Time) {
			if len(m) > MaxFrame {
				t.Fatalf("frame of %d bytes", len(m))
			}
			total += len(m)
		})
		if cut < 0 {
			cut = -cut
		}
		cut %= len(b) + 1
		fr.Write(b[:cut], now)
		fr.Write(b[cut:], now)
		fr.Close()
		if total > len(b)*3+64 {
			t.Fatalf("emitted %d bytes from %d", total, len(b))
		}
	})
}

func TestMessageEntries(t *testing.T) {
	m := Parse([]byte("<11>1 2026-11-16T10:00:00Z - app 7 ID1 [a b=\"c\"] line one\nline two\n"), now, time.UTC)
	es := MessageEntries(m, testIP, now, "syslog:lab/dut1")
	if len(es) != 2 {
		t.Fatalf("%d entries", len(es))
	}
	e := es[0]
	if e.Stream != "syslog:lab/dut1" || e.Host != "192.0.2.7" || e.App != "app" || e.Severity != 3 || e.Text != "line one" || es[1].Text != "line two" {
		t.Errorf("entry %+v", e)
	}
	if e.SrcTS == nil || !e.SrcTS.Equal(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("src_ts %v", e.SrcTS)
	}
	var meta map[string]any
	if err := json.Unmarshal(e.Meta, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["facility"] != float64(1) || meta["facility_name"] != "user" || meta["proc_id"] != "7" || meta["msg_id"] != "ID1" {
		t.Errorf("meta %s", e.Meta)
	}
	if sd, _ := meta["sd"].(map[string]any); sd["a"].(map[string]any)["b"] != "c" {
		t.Errorf("sd %s", e.Meta)
	}

	raw := MessageEntries(Parse([]byte("no pri at all"), now, time.UTC), testIP, now, "syslog:lab/dut1")
	if raw[0].Severity != store.DefaultSeverity || raw[0].Text != "no pri at all" || raw[0].Host != "192.0.2.7" {
		t.Errorf("raw %+v", raw[0])
	}

	weird := MessageEntries(Parse([]byte("<13>1 - h*st?[x] a - - - x"), now, time.UTC), testIP, now, "syslog:lab/dut1")
	if err := store.ValidateStreamID("syslog:" + weird[0].Host); err != nil {
		t.Errorf("hostile hostname: %v", err)
	}

	big := Message{Format: FormatRFC5424, Facility: 1, Severity: 6, Hostname: "h", SD: map[string]map[string]string{}}
	for i := range 200 {
		big.SD[fmt.Sprintf("id%03d", i)] = map[string]string{"k": strings.Repeat("v", 40)}
	}
	be := MessageEntries(big, testIP, now, "syslog:lab/dut1")[0]
	if len(be.Meta) > store.MaxMetaBytes || !json.Valid(be.Meta) || !strings.Contains(string(be.Meta), `"truncated":true`) {
		t.Errorf("meta of %d bytes, valid %v", len(be.Meta), json.Valid(be.Meta))
	}
}

type harness struct {
	w      *ingesttest.Writer
	st     *ingesttest.Status
	udp    net.Addr
	tcp    net.Addr
	cancel context.CancelFunc
	done   chan error
}

// local is the sink the test senders, all on 127.0.0.1, land in.
const local = "syslog:lab/local"

func startSyslog(t *testing.T, allow []string, prep func(*ingesttest.Writer), sinks ...string) *harness {
	t.Helper()
	h := &harness{w: ingesttest.NewWriter(), st: ingesttest.NewStatus(), done: make(chan error, 1)}
	if prep != nil {
		prep(h.w)
	}
	if len(sinks) == 0 {
		sinks = []string{"lab/local=127.0.0.1"}
	}
	d := ingesttest.Deps(h.w, h.st)
	d.Projects = ingesttest.NewSinks(t, sinks...).Service
	s, err := New(d, allow)
	if err != nil {
		t.Fatal(err)
	}
	bound := make(chan struct{})
	s.Bound = func(u, tc net.Addr) { h.udp, h.tcp = u, tc; close(bound) }
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- s.Run(ctx, "127.0.0.1:0", "127.0.0.1:0") }()
	select {
	case <-bound:
	case err := <-h.done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("not bound")
	}
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		panic("syslog server did not stop")
	}
}

func send(t *testing.T, network string, addr net.Addr, data ...string) {
	t.Helper()
	c, err := net.Dial(network, addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, d := range data {
		if _, err := c.Write([]byte(d)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUDPAndTCPListeners(t *testing.T) {
	h := startSyslog(t, nil, nil)
	send(t, "udp", h.udp, "<11>1 - dut1 app - - - over udp\n")
	msg := "<13>1 - dut2 a - - - x"
	send(t, "tcp", h.tcp, fmt.Sprintf("%d %s", len(msg), msg), "<14>not a frame") // falls back after the first frame
	es := h.w.Wait(3, 5*time.Second)
	got := map[string]string{}
	for _, e := range es {
		if e.Stream != local {
			t.Errorf("entry in %s", e.Stream)
		}
		got[e.Host] += e.Text + "|"
	}
	if got["dut1"] != "over udp|" || got["dut2"] != "x|" || got["127.0.0.1"] != "not a frame|" {
		t.Fatalf("entries %v", got)
	}
	// The TCP peer went away: its sink's stream is down.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var up, known bool
		h.st.Get(func(s *ingesttest.Status) { up, known = s.Connected[local] })
		if known && !up {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TCP stream never marked down")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUDPDropsAreCounted(t *testing.T) {
	h := startSyslog(t, nil, func(w *ingesttest.Writer) { w.SetFull(true) })
	for range 5 {
		send(t, "udp", h.udp, "<11>1 - lossy a - - - x")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		h.st.Get(func(s *ingesttest.Status) { n = s.Drops[local] })
		if n == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("drops = %d, want 5", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAllowlist(t *testing.T) {
	h := startSyslog(t, []string{"10.0.0.0/8"}, nil)
	send(t, "udp", h.udp, "<11>refused")
	c, err := net.Dial("tcp", h.tcp.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("a refused TCP peer was not disconnected")
	}
	c.Close()
	time.Sleep(100 * time.Millisecond)
	if es := h.w.Entries(); len(es) != 0 {
		t.Fatalf("entries from a refused source: %v", es)
	}

	h2 := startSyslog(t, []string{"127.0.0.1"}, nil)
	send(t, "udp", h2.udp, "<11>welcome")
	if es := h2.w.Wait(1, 5*time.Second); len(es) != 1 {
		t.Fatal("allowed source refused")
	}
}

func TestSendersNoSinkListsAreDropped(t *testing.T) {
	h := startSyslog(t, nil, nil, "lab/far=10.0.0.0/8")
	send(t, "udp", h.udp, "<11>1 - dut1 app - - - nobody wants this")
	send(t, "tcp", h.tcp, "<11>1 - dut1 app - - - nor this\n")
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		h.st.Get(func(s *ingesttest.Status) { n = s.Unrouteds["syslog"] })
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unrouted = %d, want 2", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if es := h.w.Entries(); len(es) != 0 {
		t.Fatalf("entries from an unlisted sender: %v", es)
	}
}

func TestConfig(t *testing.T) {
	var c Config
	c.SetDefaults()
	if c.UDP != ":5514" || c.TCP != ":5514" || c.Validate() != nil {
		t.Fatalf("defaults %+v", c)
	}
	c.Allow = []string{"10.0.50.0/24", "192.0.2.1", "fe80::/10"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Config{{UDP: "nope"}, {TCP: ":1", Allow: []string{"10.0.0.0/33"}}, {Allow: []string{"host.lan"}}} {
		if bad.Validate() == nil {
			t.Errorf("%+v validated", bad)
		}
	}
	if (&Config{}).Validate() != nil {
		t.Error("both listeners disabled must be valid")
	}
}
