package serial

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/ingest/ingestutil/ingesttest"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/testutil"
)

// fakeClaims is the claim side of messaging: resource -> holder.
type fakeClaims struct {
	mu     sync.Mutex
	holder map[string]string
}

func (f *fakeClaims) Send(context.Context, auth.Principal, dto.SendRequest) (dto.Entry, error) {
	return dto.Entry{}, errors.New("not here")
}
func (f *fakeClaims) Visible(context.Context, auth.Principal, store.Entry) bool { return false }
func (f *fakeClaims) ResolveChannel(context.Context, string) ([]string, error) {
	return nil, errors.New("not here")
}
func (f *fakeClaims) ChatOf(store.Entry) string { return "" }

func (f *fakeClaims) Holds(_ context.Context, p auth.Principal, resource string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holder[resource] == p.Name, nil
}

func (f *fakeClaims) ActiveClaims(context.Context) (map[string]dto.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]dto.Claim{}
	for r, p := range f.holder {
		out[r] = dto.Claim{Principal: p, ExpiresAt: dto.TimeOf(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC))}
	}
	return out, nil
}

func (f *fakeClaims) set(resource, p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p == "" {
		delete(f.holder, resource)
	} else {
		f.holder[resource] = p
	}
}

func wantCode(t *testing.T, what string, err error, code string, contains ...string) {
	t.Helper()
	var ae *dto.APIError
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("%s: %v, want %s", what, err, code)
	}
	for _, c := range contains {
		if !strings.Contains(ae.Message, c) {
			t.Errorf("%s: %q lacks %q", what, ae.Message, c)
		}
	}
}

func markers(w *ingesttest.Writer) []string {
	var out []string
	for _, e := range w.Entries() {
		if e.Kind == store.KindMarker && !strings.HasPrefix(e.Text, "serial: connected") && !strings.HasPrefix(e.Text, "serial: cannot open") {
			out = append(out, e.Text)
		}
	}
	return out
}

func TestToolsGateWriteAndControl(t *testing.T) {
	e := newEnv()
	claims := &fakeClaims{holder: map[string]string{}}
	e.deps.Messaging = claims
	o := newOpens()
	var link *fakeLink
	far := o.pipe(func(c net.Conn) io.ReadWriteCloser { link = &fakeLink{Conn: c}; return link })
	tcpFar := make(chan net.Conn, 1)
	f := func(ctx context.Context, p Port, ev Events) (io.ReadWriteCloser, error) {
		switch p.Name {
		case "dut3", "ro":
			a, b := net.Pipe()
			t.Cleanup(func() { b.Close() })
			if p.Name == "dut3" {
				select {
				case tcpFar <- b:
				default:
				}
			}
			return a, nil
		case "gone":
			return nil, errors.New("no such file or directory")
		}
		return o.factory(ctx, p, ev)
	}
	m := startManager(t, e, []Port{
		{Name: "dut1", Device: "/dev/ttyUSB0", Writable: true},
		{Name: "dut3", TCP: "10.0.50.30:7002", Writable: true},
		{Name: "ro", TCP: "10.0.50.30:7003"},
		{Name: "gone", Device: "/dev/ttyUSB9", Writable: true},
	}, f)
	tl := newTools(m)
	b := <-far
	<-tcpFar
	for _, s := range []string{"serial:dut1", "serial:dut3", "serial:ro"} {
		h, _ := m.Lookup(s)
		testutil.Eventually(t, s+" connected", 3*time.Second, func() bool { ok, _ := h.Connected(); return ok })
	}
	gone, _ := m.Lookup("serial:gone")
	testutil.Eventually(t, "gone tried", 3*time.Second, func() bool { _, why := gone.Connected(); return why != "" })

	ctx := context.Background()
	a := &mcpserver.Call{Principal: auth.Principal{Name: "agent-a", Scopes: auth.NewScopeSet(auth.ScopeSerialWrite)}}
	bb := &mcpserver.Call{Principal: auth.Principal{Name: "agent-b", Scopes: auth.NewScopeSet(auth.ScopeSerialWrite)}}
	no := false

	_, _, err := tl.write(ctx, a, writeIn{Stream: "serial:nope", Data: "x"})
	wantCode(t, "unknown", err, dto.CodeNotFound, "serial:dut1")
	_, _, err = tl.write(ctx, a, writeIn{Stream: "serial:ro", Data: "x"})
	wantCode(t, "read-only", err, dto.CodeForbidden, "writable: true")
	_, _, err = tl.control(ctx, a, controlIn{Stream: "serial:ro", DTR: &no})
	wantCode(t, "read-only control", err, dto.CodeForbidden, "writable: true")
	_, _, err = tl.write(ctx, a, writeIn{Stream: "serial:dut1", Data: "x"})
	wantCode(t, "unclaimed", err, dto.CodeForbidden, "Claim serial:dut1 first")

	claims.set("serial:dut1", "agent-a")
	_, _, err = tl.write(ctx, bb, writeIn{Stream: "serial:dut1", Data: "x"})
	wantCode(t, "other holder", err, dto.CodeConflict, "agent-a")
	_, _, err = tl.control(ctx, bb, controlIn{Stream: "serial:dut1", DTR: &no})
	wantCode(t, "other holder control", err, dto.CodeConflict, "agent-a")
	if len(markers(e.w)) != 0 {
		t.Fatalf("refusals left markers: %q", markers(e.w))
	}

	got := make(chan []byte, 1)
	go func() { buf := make([]byte, 64); n, _ := b.Read(buf); got <- buf[:n] }()
	_, out, err := tl.write(ctx, a, writeIn{Stream: "serial:dut1", Data: "reboot"})
	if err != nil || out.BytesWritten != 7 {
		t.Fatalf("write = %+v, %v", out, err)
	}
	if g := <-got; string(g) != "reboot\r" {
		t.Errorf("port got %q", g)
	}
	go func() { buf := make([]byte, 64); n, _ := b.Read(buf); got <- buf[:n] }()
	empty := ""
	if _, out, err = tl.write(ctx, a, writeIn{Stream: "serial:dut1", Data: "\x03", Newline: &empty}); err != nil || out.BytesWritten != 1 {
		t.Fatalf("ctrl-c = %+v, %v", out, err)
	}
	<-got

	_, cout, err := tl.control(ctx, a, controlIn{Stream: "serial:dut1", DTR: &no, Pulse: &pulseIn{Line: "RTS", MS: 5}})
	if err != nil {
		t.Fatal(err)
	}
	link.mu.Lock()
	calls := slices.Clone(link.calls)
	link.mu.Unlock()
	if want := []string{"dtr off", "rts off", "rts on"}; !slices.Equal(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}
	if cout.Lines.DTR == nil || *cout.Lines.DTR || cout.Lines.RTS == nil || !*cout.Lines.RTS || cout.Baud != DefaultBaud {
		t.Errorf("control out %+v", cout)
	}
	// An empty request reads the state and changes nothing.
	if _, cout, err = tl.control(ctx, a, controlIn{Stream: "serial:dut1"}); err != nil || cout.Lines.DTR == nil || *cout.Lines.DTR {
		t.Errorf("read state = %+v, %v", cout, err)
	}
	_, _, err = tl.control(ctx, a, controlIn{Stream: "serial:dut1", Pulse: &pulseIn{Line: "cts", MS: 5}})
	wantCode(t, "bad pulse", err, dto.CodeBadRequest, "pulse.line")

	claims.set("serial:dut3", "agent-a")
	_, _, err = tl.control(ctx, a, controlIn{Stream: "serial:dut3", DTR: &no})
	wantCode(t, "raw tcp", err, dto.CodeBadRequest, "raw TCP carries data only")

	claims.set("serial:gone", "agent-a")
	_, _, err = tl.write(ctx, a, writeIn{Stream: "serial:gone", Data: "x"})
	wantCode(t, "disconnected", err, dto.CodeBusy, "no such file or directory", "try again")
	var ae *dto.APIError
	if errors.As(err, &ae) && ae.RetryAfterS == 0 {
		t.Error("busy without retry_after_s")
	}

	want := []string{
		`agent-a> reboot\r`,
		`agent-a> \x03`,
		"serial: agent-a set DTR off, pulsed RTS for 5 ms",
	}
	if got := markers(e.w); !slices.Equal(got, want) {
		t.Errorf("markers:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, en := range e.w.Entries() {
		if en.Kind == store.KindMarker && strings.HasPrefix(en.Text, "agent-a") && (en.Sender != "agent-a" || en.Stream != "serial:dut1" || en.Severity != store.DefaultSeverity) {
			t.Errorf("marker %+v", en)
		}
	}
}

func TestToolsWithoutClaimsRefuse(t *testing.T) {
	e := newEnv()
	o := newOpens()
	o.pipe(nil)
	m := startManager(t, e, []Port{{Name: "dut1", Device: "/dev/ttyUSB0", Writable: true}}, o.factory)
	tl := newTools(m)
	a := &mcpserver.Call{Principal: auth.Principal{Name: "agent-a"}}
	_, _, err := tl.write(context.Background(), a, writeIn{Stream: "serial:dut1", Data: "x"})
	wantCode(t, "no messaging", err, dto.CodeForbidden, "no claims")
}

func TestEscape(t *testing.T) {
	cases := map[string]string{
		"ls -l\r":               `ls -l\r`,
		"a\\b\tc\n":             `a\\b\tc\n`,
		"\x1b[A\x00\x7f":        `\x1b[A\x00\x7f`,
		"caf\xc3\xa9 \xff":      `café \xff`,
		"x\u202ey":              `x\u202ey`,
		strings.Repeat("a", 30): "aaaaaaaaaaaaaaaaaaaa... (30 bytes in all)",
	}
	for in, want := range cases {
		if got := Escape([]byte(in), 20); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
	}
}
