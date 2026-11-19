package serial

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/ingest/ingestutil/ingesttest"
	"github.com/nebuloss/cafard/internal/ingest/serial/rfc2217"
	"github.com/nebuloss/cafard/internal/ingest/serial/rfc2217/rfc2217test"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/testutil"
)

// texts lists "kind:text" for every entry.
func texts(w *ingesttest.Writer) []string {
	var out []string
	for _, e := range w.Entries() {
		out = append(out, string(e.Kind)+":"+e.Text)
	}
	return out
}

type env struct {
	w    *ingesttest.Writer
	st   *status.Memory
	deps deps.Deps
}

func newEnv() *env {
	e := &env{w: ingesttest.NewWriter(), st: status.New(clock.System{})}
	e.deps = deps.Deps{Writer: e.w, Status: e.st, Clock: clock.System{}}
	return e
}

// opens hands out what the fake factory returns, one per open.
type opens struct {
	mu    sync.Mutex
	calls []Port
	next  chan func(p Port, ev Events) (io.ReadWriteCloser, error)
}

func newOpens() *opens {
	return &opens{next: make(chan func(Port, Events) (io.ReadWriteCloser, error), 8)}
}

func (o *opens) factory(ctx context.Context, p Port, ev Events) (io.ReadWriteCloser, error) {
	o.mu.Lock()
	o.calls = append(o.calls, p)
	o.mu.Unlock()
	select {
	case f := <-o.next:
		return f(p, ev)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (o *opens) fail(err error) {
	o.next <- func(Port, Events) (io.ReadWriteCloser, error) { return nil, err }
}

// pipe makes the next open succeed and returns the far end.
func (o *opens) pipe(wrap func(net.Conn) io.ReadWriteCloser) <-chan net.Conn {
	far := make(chan net.Conn, 1)
	o.next <- func(Port, Events) (io.ReadWriteCloser, error) {
		a, b := net.Pipe()
		far <- b
		if wrap != nil {
			return wrap(a), nil
		}
		return a, nil
	}
	return far
}

func startManager(t *testing.T, e *env, ports []Port, f Factory) *Manager {
	t.Helper()
	m, err := New(e.deps, &Config{Ports: ports}, Options{Factory: f, MinBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond, TickEvery: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = m.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return m
}

func hasEntry(w *ingesttest.Writer, want string) bool {
	return slices.ContainsFunc(texts(w), func(s string) bool { return strings.Contains(s, want) })
}

func TestReconnectWritesMarkersAndStatus(t *testing.T) {
	e := newEnv()
	o := newOpens()
	o.fail(errors.New("no such file or directory"))
	far := o.pipe(nil)
	startManager(t, e, []Port{{Name: "dut1", TCP: "10.0.50.30:7002"}}, o.factory)

	b := <-far
	go b.Write([]byte("hello\r\nboot\rlogin: "))
	testutil.Eventually(t, "lines", 3*time.Second, func() bool { return hasEntry(e.w, "line:login: ") })
	got, _ := e.st.Get("serial:dut1")
	if got.Status != dto.StreamConnected && got.Status != dto.StreamIdle {
		t.Errorf("status while up = %s", got.Status)
	}
	if got.Transport != "tcp" {
		t.Errorf("transport = %q", got.Transport)
	}

	far2 := o.pipe(nil)
	b.Close()
	testutil.Eventually(t, "disconnect marker", 3*time.Second, func() bool { return hasEntry(e.w, "marker:serial: disconnected from 10.0.50.30:7002") })
	b2 := <-far2
	testutil.Eventually(t, "reconnect marker", 3*time.Second, func() bool { return hasEntry(e.w, "marker:serial: reconnected after") })
	go b2.Write([]byte("again\n"))
	testutil.Eventually(t, "line after reconnect", 3*time.Second, func() bool { return hasEntry(e.w, "line:again") })

	want := []string{
		"marker:serial: cannot open 10.0.50.30:7002: no such file or directory; retrying",
		"marker:serial: connected: 10.0.50.30:7002",
		"line:hello",
		"line:boot",
		"line:login: ",
		"marker:serial: disconnected",
		"marker:serial: reconnected",
		"line:again",
	}
	texts := texts(e.w)
	if len(texts) != len(want) {
		t.Fatalf("entries:\n%s", strings.Join(texts, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(texts[i], w) {
			t.Errorf("entry %d = %q, want %q...", i, texts[i], w)
		}
	}
	for _, en := range e.w.Entries() {
		if en.Stream != "serial:dut1" {
			t.Errorf("stream %q", en.Stream)
		}
		if en.Kind == store.KindLine && en.Severity != store.DefaultSeverity {
			t.Errorf("line severity %d", en.Severity)
		}
		if en.Kind == store.KindMarker {
			var meta map[string]string
			if json.Unmarshal(en.Meta, &meta) != nil || meta["serial"] == "" {
				t.Errorf("marker meta %s", en.Meta)
			}
		}
	}
}

func TestDownStatusCarriesTheReason(t *testing.T) {
	e := newEnv()
	o := newOpens()
	for range 3 {
		o.fail(errors.New("permission denied"))
	}
	startManager(t, e, []Port{{Name: "dut1", Device: "/dev/ttyUSB0"}}, o.factory)
	testutil.Eventually(t, "three attempts", 3*time.Second, func() bool { o.mu.Lock(); defer o.mu.Unlock(); return len(o.calls) >= 3 })
	got, _ := e.st.Get("serial:dut1")
	if got.Status != dto.StreamDown {
		t.Errorf("status = %s", got.Status)
	}
	// One marker for the whole streak of failures.
	n := 0
	for _, s := range texts(e.w) {
		if strings.Contains(s, "cannot open") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d open_failed markers: %q", n, texts(e.w))
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.calls[0].Baud != DefaultBaud || o.calls[0].Parity != "none" || o.calls[0].StopBits != 1 || o.calls[0].DataBits != 8 {
		t.Errorf("defaults not applied: %+v", o.calls[0])
	}
}

// fakeLink is a pipe that also drives lines, recording each call.
type fakeLink struct {
	net.Conn
	mu    sync.Mutex
	calls []string
}

func (f *fakeLink) rec(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
	return nil
}

func (f *fakeLink) SetDTR(_ context.Context, on bool) error {
	return f.rec(map[bool]string{true: "dtr on", false: "dtr off"}[on])
}
func (f *fakeLink) SetRTS(_ context.Context, on bool) error {
	return f.rec(map[bool]string{true: "rts on", false: "rts off"}[on])
}
func (f *fakeLink) Break(_ context.Context, d time.Duration) error {
	return f.rec("break " + d.String())
}
func (f *fakeLink) SetBaud(_ context.Context, b int) error {
	if b == 1234 {
		return errors.New("unsupported rate")
	}
	return f.rec("baud")
}

func TestControlDrivesTheLinesInOrder(t *testing.T) {
	e := newEnv()
	o := newOpens()
	var link *fakeLink
	far := o.pipe(func(c net.Conn) io.ReadWriteCloser { link = &fakeLink{Conn: c}; return link })
	m := startManager(t, e, []Port{{Name: "dut1", Device: "/dev/ttyUSB0", Writable: true}}, o.factory)
	b := <-far
	h, err := m.Lookup("serial:dut1")
	if err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, "connected", 3*time.Second, func() bool { ok, _ := h.Connected(); return ok })

	yes, no := true, false
	ls, err := h.Control(context.Background(), ControlRequest{Baud: 9600, DTR: &yes, RTS: &no, BreakMS: 5, Pulse: &Pulse{Line: "dtr", MS: 5}})
	if err != nil {
		t.Fatal(err)
	}
	link.mu.Lock()
	calls := slices.Clone(link.calls)
	link.mu.Unlock()
	want := []string{"baud", "dtr on", "rts off", "break 5ms", "dtr off", "dtr on"}
	if !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
	if ls.DTR == nil || !*ls.DTR || ls.RTS == nil || *ls.RTS {
		t.Errorf("line state %+v", ls)
	}
	st, _ := e.st.Get("serial:dut1")
	if st.LineState == nil || st.LineState.RTS == nil || *st.LineState.RTS {
		t.Errorf("status line state %+v", st.LineState)
	}

	if _, err := h.Control(context.Background(), ControlRequest{Baud: 1234}); err == nil || !strings.Contains(err.Error(), "unsupported rate") {
		t.Errorf("failed baud: %v", err)
	}
	for _, bad := range []ControlRequest{{BreakMS: -1}, {BreakMS: 20000}, {Pulse: &Pulse{Line: "cts", MS: 5}}, {Pulse: &Pulse{Line: "dtr"}}, {Baud: 3}} {
		if _, err := h.Control(context.Background(), bad); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	// Write goes to the port as is.
	got := make(chan []byte, 1)
	go func() { buf := make([]byte, 16); n, _ := b.Read(buf); got <- buf[:n] }()
	if n, err := h.Write(context.Background(), []byte("reboot\r")); err != nil || n != 7 {
		t.Fatalf("write = %d, %v", n, err)
	}
	if g := <-got; string(g) != "reboot\r" {
		t.Errorf("far end read %q", g)
	}

	// The new baud rate survives a reconnect.
	far2 := o.pipe(nil)
	b.Close()
	<-far2
	testutil.Eventually(t, "reopen", 3*time.Second, func() bool { o.mu.Lock(); defer o.mu.Unlock(); return len(o.calls) == 2 })
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.calls[1].Baud != 9600 {
		t.Errorf("reopened at %d baud", o.calls[1].Baud)
	}
}

func TestRawTCPRefusesControl(t *testing.T) {
	e := newEnv()
	o := newOpens()
	far := o.pipe(nil)
	f := func(ctx context.Context, p Port, ev Events) (io.ReadWriteCloser, error) {
		if p.Name == "ro" {
			a, b := net.Pipe()
			t.Cleanup(func() { b.Close() })
			return a, nil
		}
		return o.factory(ctx, p, ev)
	}
	m := startManager(t, e, []Port{{Name: "dut3", TCP: "10.0.50.30:7002", Writable: true}, {Name: "ro", TCP: "10.0.50.30:7003"}}, f)
	<-far
	h, _ := m.Lookup("serial:dut3")
	testutil.Eventually(t, "connected", 3*time.Second, func() bool { ok, _ := h.Connected(); return ok })
	yes := true
	_, err := h.Control(context.Background(), ControlRequest{DTR: &yes})
	var ue *UnsupportedError
	if !errors.As(err, &ue) || !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "raw TCP") {
		t.Fatalf("control on raw tcp: %v", err)
	}
	// Reading the line state is not control.
	if _, err := h.Control(context.Background(), ControlRequest{}); err != nil {
		t.Errorf("empty request: %v", err)
	}

	ro, _ := m.Lookup("serial:ro")
	if _, err := ro.Write(context.Background(), []byte("x")); !errors.Is(err, ErrNotWritable) {
		t.Errorf("write to a read-only port: %v", err)
	}
	if _, err := ro.Control(context.Background(), ControlRequest{DTR: &yes}); !errors.Is(err, ErrNotWritable) {
		t.Errorf("control of a read-only port: %v", err)
	}
	if _, err := m.Lookup("serial:nope"); !errors.Is(err, ErrUnknownStream) {
		t.Errorf("unknown stream: %v", err)
	}
}

func TestWriteToADisconnectedPort(t *testing.T) {
	e := newEnv()
	o := newOpens()
	o.fail(errors.New("unplugged"))
	m := startManager(t, e, []Port{{Name: "dut1", Device: "/dev/ttyUSB0", Writable: true}}, o.factory)
	h, _ := m.Lookup("serial:dut1")
	testutil.Eventually(t, "attempt", 3*time.Second, func() bool { _, why := h.Connected(); return why == "unplugged" })
	if _, err := h.Write(context.Background(), []byte("x")); !errors.Is(err, ErrDisconnected) || !strings.Contains(err.Error(), "unplugged") {
		t.Errorf("write: %v", err)
	}
}

// rfcPeer is an RFC 2217 peer on the far end of a pipe.
type rfcPeer struct {
	*rfc2217test.Peer
	conn net.Conn
}

func rfcFactory(peers chan rfcPeer, refuse bool) Factory {
	return func(ctx context.Context, p Port, ev Events) (io.ReadWriteCloser, error) {
		a, b := net.Pipe()
		s := rfcPeer{&rfc2217test.Peer{Refuse: refuse}, b}
		go s.Serve(b)
		peers <- s
		return newRFC2217(ctx, a, p, ev)
	}
}

func TestRFC2217PortSetsBaudAndPulsesDTR(t *testing.T) {
	e := newEnv()
	servers := make(chan rfcPeer, 4)
	m := startManager(t, e, []Port{{Name: "dut2", RFC2217: "10.0.50.30:7001", Baud: 57600, Writable: true}}, rfcFactory(servers, false))
	s := <-servers
	h, _ := m.Lookup("serial:dut2")
	testutil.Eventually(t, "connected", 3*time.Second, func() bool { ok, _ := h.Connected(); return ok })
	testutil.Eventually(t, "baud", 3*time.Second, func() bool { return s.Baud() != nil })
	if b := s.Baud(); !bytes.Equal(b, []byte{0, 0, 0xE1, 0}) {
		t.Errorf("SET-BAUDRATE % x, want 57600", b)
	}
	testutil.Eventually(t, "connect marker", 3*time.Second, func() bool { return hasEntry(e.w, "marker:serial: connected: RFC 2217 10.0.50.30:7001 at 57600 8N1") })

	ls, err := h.Control(context.Background(), ControlRequest{Pulse: &Pulse{Line: "dtr", MS: 5}})
	if err != nil {
		t.Fatal(err)
	}
	ctl := s.Control()
	if !bytes.Equal(ctl, []byte{rfc2217.ControlDTROff, rfc2217.ControlDTROn}) {
		t.Errorf("SET-CONTROL values %v", ctl)
	}
	if ls.DTR == nil || !*ls.DTR {
		t.Errorf("line state %+v", ls)
	}

	// Carrier changes become markers.
	go s.conn.Write([]byte{rfc2217.IAC, rfc2217.SB, rfc2217.OptComPort, 107, 0x80, rfc2217.IAC, rfc2217.SE,
		rfc2217.IAC, rfc2217.SB, rfc2217.OptComPort, 107, 0x08, rfc2217.IAC, rfc2217.SE})
	testutil.Eventually(t, "carrier marker", 3*time.Second, func() bool { return hasEntry(e.w, "marker:serial: carrier detect (DCD) dropped") })
	if got := h.LineState(); got.DCD == nil || *got.DCD {
		t.Errorf("DCD = %v", got.DCD)
	}
}

func TestRFC2217RefusalIsExplained(t *testing.T) {
	e := newEnv()
	servers := make(chan rfcPeer, 4)
	m := startManager(t, e, []Port{{Name: "dut2", RFC2217: "10.0.50.30:7001", Writable: true}}, rfcFactory(servers, true))
	s := <-servers
	h, _ := m.Lookup("serial:dut2")
	testutil.Eventually(t, "connected", 3*time.Second, func() bool { ok, _ := h.Connected(); return ok })
	testutil.Eventually(t, "plain Telnet marker", 3*time.Second, func() bool {
		return hasEntry(e.w, "marker:serial: connected: plain Telnet 10.0.50.30:7001: the peer answered DONT COM-PORT-OPTION")
	})
	yes := true
	if _, err := h.Control(context.Background(), ControlRequest{DTR: &yes}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "plain Telnet") {
		t.Errorf("control: %v", err)
	}
	// Data still flows.
	go s.conn.Write([]byte("still here\n"))
	testutil.Eventually(t, "data", 3*time.Second, func() bool { return hasEntry(e.w, "line:still here") })
}

func TestConfigYAML(t *testing.T) {
	var root struct {
		Serial Config `yaml:"serial"`
	}
	src := `
serial:
  - name: dut1
    device: /dev/serial/by-id/usb-FTDI_FT232R_A1B2C3-if00-port0
    baud: 9600
    parity: even
    stop_bits: 1.5
    writable: true
  - name: dut2
    rfc2217: 10.0.50.30:7001
  - name: dut3
    tcp: 10.0.50.30:7002
`
	if err := yaml.Unmarshal([]byte(src), &root); err != nil {
		t.Fatal(err)
	}
	if err := root.Serial.Validate(); err != nil {
		t.Fatal(err)
	}
	p := root.Serial.Ports
	if len(p) != 3 || p[0].Framing() != "9600 8E1.5" || !p[0].Writable || p[1].Framing() != "115200 8N1" || p[2].Baud != 0 {
		t.Fatalf("%+v", p)
	}
	if p[0].Transport() != "local" || p[1].Transport() != "rfc2217" || p[2].Transport() != "tcp" {
		t.Errorf("transports %s %s %s", p[0].Transport(), p[1].Transport(), p[2].Transport())
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "rfc2217: 10.0.50.30:7001") {
		t.Errorf("marshal:\n%s", out)
	}

	err = yaml.Unmarshal([]byte("serial:\n  - name: x\n    device: /dev/ttyS0\n    bauds: 9600\n"), &root)
	if err == nil || !strings.Contains(err.Error(), "bauds") {
		t.Errorf("unknown key: %v", err)
	}
	if err := yaml.Unmarshal([]byte("serial: {}\n"), &root); err == nil {
		t.Error("a mapping was accepted as the list")
	}
}

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		port Port
		err  string
	}{
		{Port{Name: "ok", Device: "COM5"}, ""},
		{Port{Name: "", Device: "COM5"}, "name"},
		{Port{Name: "Dut", Device: "COM5"}, "name"},
		{Port{Name: "a b", Device: "COM5"}, "name"},
		{Port{Name: "x"}, "exactly one"},
		{Port{Name: "x", Device: "COM5", TCP: "h:1"}, "exactly one"},
		{Port{Name: "x", TCP: "nohost"}, "host:port"},
		{Port{Name: "x", RFC2217: "h:99999"}, "port"},
		{Port{Name: "x", TCP: "h:1", Baud: 9600}, "raw tcp"},
		{Port{Name: "x", Device: "COM5", DataBits: 9}, "data_bits"},
		{Port{Name: "x", Device: "COM5", Parity: "high"}, "parity"},
		{Port{Name: "x", Device: "COM5", StopBits: 3}, "stop_bits"},
		{Port{Name: "x", Device: "COM5", Baud: 10}, "baud"},
	}
	for _, c := range cases {
		cfg := Config{Ports: []Port{c.port}}
		cfg.SetDefaults()
		err := cfg.Validate()
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%+v: %v, want %q", c.port, err, c.err)
		}
	}
	dup := Config{Ports: []Port{{Name: "a", Device: "x"}, {Name: "a", Device: "y"}}}
	dup.SetDefaults()
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "two ports") {
		t.Errorf("duplicate names: %v", err)
	}
}

func TestReconcileFollowsTheSinks(t *testing.T) {
	e := newEnv()
	o := newOpens()
	m := startManager(t, e, nil, o.factory)
	const stream = "serial:lab/dut1"
	far := o.pipe(nil)
	m.Reconcile([]Port{{Name: "lab/dut1", Device: "/dev/ttyUSB0"}, {Name: "lab/bad"}})
	t.Cleanup(func() { (<-far).Close() })
	h, err := m.Lookup(stream)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, "connected", 3*time.Second, func() bool { ok, _ := h.Connected(); return ok })
	if _, err := m.Lookup("serial:lab/bad"); err == nil {
		t.Error("a port without an address runs")
	}

	// The same settings leave the port alone; new ones reopen it.
	m.Reconcile([]Port{{Name: "lab/dut1", Device: "/dev/ttyUSB0"}})
	if again, _ := m.Lookup(stream); again != h {
		t.Error("an unchanged port was reopened")
	}
	far2 := o.pipe(nil)
	m.Reconcile([]Port{{Name: "lab/dut1", Device: "/dev/ttyUSB0", Baud: 9600}})
	t.Cleanup(func() { (<-far2).Close() })
	h2, _ := m.Lookup(stream)
	testutil.Eventually(t, "reopened", 3*time.Second, func() bool { ok, _ := h2.Connected(); return ok })
	o.mu.Lock()
	baud := o.calls[len(o.calls)-1].Baud
	o.mu.Unlock()
	if h2 == h || baud != 9600 {
		t.Errorf("reopened with baud %d", baud)
	}

	m.Reconcile(nil)
	if _, err := m.Lookup(stream); !errors.Is(err, ErrUnknownStream) {
		t.Errorf("a removed port is still there: %v", err)
	}
	if !hasEntry(e.w, "serial: port settings changed") || !hasEntry(e.w, "serial: port removed") {
		t.Errorf("markers = %q", texts(e.w))
	}
	if st, ok := e.st.Get(stream); !ok || st.Status != dto.StreamDown {
		t.Errorf("status after removal = %+v", st)
	}
}
