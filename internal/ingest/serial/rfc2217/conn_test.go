package rfc2217

import (
	"bytes"
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"
)

// peer is an in-process Telnet server on the far end of a net.Pipe.
type peer struct {
	t    *testing.T
	conn net.Conn

	mu    sync.Mutex
	raw   []byte
	data  []byte
	verbs [][2]byte
	subs  [][]byte

	// onVerb, when set, answers the client's option requests.
	onVerb func(p *peer, verb, opt byte)
	// onSub, when set, answers COM-PORT-OPTION commands.
	onSub func(p *peer, body []byte)
}

func newPair(t *testing.T, opts Options, setup func(p *peer)) (*Conn, *peer) {
	t.Helper()
	a, b := net.Pipe()
	p := &peer{t: t, conn: b}
	if setup != nil {
		setup(p)
	}
	go p.run()
	c := New(a, opts)
	t.Cleanup(func() { c.Close(); b.Close() })
	return c, p
}

func (p *peer) run() {
	ps := &parser{binary: true}
	ps.onVerb = func(verb, opt byte) {
		p.mu.Lock()
		p.verbs = append(p.verbs, [2]byte{verb, opt})
		p.mu.Unlock()
		if p.onVerb != nil {
			p.onVerb(p, verb, opt)
		}
	}
	ps.onSub = func(body []byte) {
		body = append([]byte(nil), body...)
		p.mu.Lock()
		p.subs = append(p.subs, body)
		p.mu.Unlock()
		if p.onSub != nil {
			p.onSub(p, body)
		}
	}
	buf := make([]byte, 1024)
	for {
		n, err := p.conn.Read(buf)
		p.mu.Lock()
		p.raw = append(p.raw, buf[:n]...)
		p.mu.Unlock()
		out := ps.feed(buf[:n], nil)
		p.mu.Lock()
		p.data = append(p.data, out...)
		p.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// send writes from the peer in a goroutine: net.Pipe is synchronous and
// the peer's reader must stay free.
func (p *peer) send(b ...byte) {
	go func() { _, _ = p.conn.Write(b) }()
}

func (p *peer) snapshot() (raw, data []byte, verbs [][2]byte, subs [][]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.raw), slices.Clone(p.data), slices.Clone(p.verbs), slices.Clone(p.subs)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ser2net answers our offers in kind and echoes COM-PORT-OPTION commands.
func ser2netLike(p *peer) {
	p.onVerb = func(p *peer, verb, opt byte) {
		switch verb {
		case WILL:
			p.send(IAC, DO, opt)
		case DO:
			if opt == OptBinary || opt == OptSGA {
				p.send(IAC, WILL, opt)
			}
		}
	}
	p.onSub = func(p *peer, body []byte) {
		if len(body) >= 2 && body[0] == OptComPort {
			p.send(subneg(body[1]+ServerOffset, body[2:])...)
		}
	}
}

func TestNegotiationSendsTheLineSettings(t *testing.T) {
	c, p := newPair(t, Options{Baud: 115200, DataBits: 8, Parity: ParityNone, StopBits: StopOne}, ser2netLike)
	if err := c.Negotiate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, reason := c.ComPort(); !ok {
		t.Fatalf("COM-PORT-OPTION refused: %s", reason)
	}
	want := [][]byte{
		{OptComPort, CmdSetBaudRate, 0x00, 0x01, 0xC2, 0x00},
		{OptComPort, CmdSetDataSize, 8},
		{OptComPort, CmdSetParity, ParityNone},
		{OptComPort, CmdSetStopSize, StopOne},
	}
	eventually(t, "the four settings", func() bool { _, _, _, s := p.snapshot(); return len(s) >= 4 })
	raw, _, verbs, subs := p.snapshot()
	for i, w := range want {
		if !bytes.Equal(subs[i], w) {
			t.Errorf("subnegotiation %d = % x, want % x", i, subs[i], w)
		}
	}
	for _, v := range [][2]byte{{WILL, OptComPort}, {WILL, OptBinary}, {DO, OptBinary}, {WILL, OptSGA}, {DO, OptSGA}} {
		if !slices.Contains(verbs, v) {
			t.Errorf("client never sent % x", v)
		}
	}
	// The client asked first, so the peer's answers are not answered again.
	if len(verbs) != 5 {
		t.Errorf("client sent %d option commands, want the 5 offers: % x", len(verbs), verbs)
	}
	if !bytes.Contains(raw, []byte{IAC, SB, OptComPort, CmdSetBaudRate, 0x00, 0x01, 0xC2, 0x00, IAC, SE}) {
		t.Errorf("SET-BAUDRATE bytes missing from % x", raw)
	}
}

func TestSubnegotiationValuesEscapeIAC(t *testing.T) {
	got := subneg(CmdSetBaudRate, baudBytes(0x1FF))
	want := []byte{IAC, SB, OptComPort, CmdSetBaudRate, 0, 0, 1, IAC, IAC, IAC, SE}
	if !bytes.Equal(got, want) {
		t.Fatalf("% x, want % x", got, want)
	}
	// And the parser reads it back.
	var body []byte
	ps := &parser{onSub: func(b []byte) { body = append([]byte(nil), b...) }}
	ps.feed(got, nil)
	if !bytes.Equal(body, []byte{OptComPort, CmdSetBaudRate, 0, 0, 1, 0xFF}) {
		t.Fatalf("parsed % x", body)
	}
}

func TestDataIsEscapedBothWays(t *testing.T) {
	var modem []byte
	var mmu sync.Mutex
	c, p := newPair(t, Options{OnModemState: func(s byte) { mmu.Lock(); modem = append(modem, s); mmu.Unlock() }}, ser2netLike)
	if err := c.Negotiate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte{'a', 0xFF, 'b'}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "data at the peer", func() bool { _, d, _, _ := p.snapshot(); return len(d) >= 3 })
	raw, data, _, _ := p.snapshot()
	if !bytes.Equal(data, []byte{'a', 0xFF, 'b'}) || !bytes.Contains(raw, []byte{'a', IAC, IAC, 'b'}) {
		t.Errorf("peer got data % x, raw % x", data, raw)
	}

	p.send(slices.Concat(
		[]byte{'x', IAC, IAC, 'y', IAC, NOP, 'z', IAC, GA},
		subneg(CmdNotifyModemState+ServerOffset, []byte{ModemDCD | ModemDeltaDCD}),
		subneg(CmdNotifyLineState+ServerOffset, []byte{0x60}),
		[]byte{IAC, WILL, OptBinary, '\r', 0, '!'}, // already on: no answer, and NUL is data in binary mode
	)...)
	var got []byte
	buf := make([]byte, 64)
	for len(got) < 7 {
		n, err := c.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Equal(got, []byte{'x', 0xFF, 'y', 'z', '\r', 0, '!'}) {
		t.Errorf("client read % x", got)
	}
	mmu.Lock()
	defer mmu.Unlock()
	if !bytes.Equal(modem, []byte{ModemDCD | ModemDeltaDCD}) {
		t.Errorf("modem states % x", modem)
	}
}

func TestSetControlSequences(t *testing.T) {
	c, p := newPair(t, Options{}, ser2netLike)
	if err := c.Negotiate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, v := range []byte{ControlDTROn, ControlDTROff, ControlRTSOn, ControlRTSOff, ControlBreakOn, ControlBreakOff} {
		got, err := c.SetControl(context.Background(), v)
		if err != nil || got != v {
			t.Fatalf("SetControl(%d) = %d, %v", v, got, err)
		}
		raw, _, _, _ := p.snapshot()
		if want := []byte{IAC, SB, OptComPort, CmdSetControl, v, IAC, SE}; !bytes.HasSuffix(raw, want) {
			t.Errorf("SetControl(%d) sent ...% x, want % x", v, raw[max(0, len(raw)-8):], want)
		}
	}
	rate, err := c.SetBaud(context.Background(), 9600)
	if err != nil || rate != 9600 {
		t.Fatalf("SetBaud = %d, %v", rate, err)
	}
}

func TestSilentServerIsAssumedToObey(t *testing.T) {
	c, p := newPair(t, Options{ReplyTimeout: 20 * time.Millisecond}, ser2netLike)
	if err := c.Negotiate(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.onSub = nil
	p.mu.Unlock()
	got, err := c.SetControl(context.Background(), ControlDTROff)
	if err != nil || got != ControlDTROff {
		t.Fatalf("SetControl = %d, %v", got, err)
	}
}

func TestRefusingPeerIsPlainTelnet(t *testing.T) {
	c, p := newPair(t, Options{Baud: 115200}, func(p *peer) {
		p.onVerb = func(p *peer, verb, opt byte) {
			switch {
			case verb == WILL && opt == OptComPort:
				p.send(IAC, DONT, OptComPort)
			case verb == WILL:
				p.send(IAC, DO, opt)
			case verb == DO:
				p.send(IAC, WILL, opt)
			}
		}
	})
	if err := c.Negotiate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ok, reason := c.ComPort()
	if ok || reason == "" {
		t.Fatalf("ComPort = %v, %q", ok, reason)
	}
	if _, err := c.SetControl(context.Background(), ControlDTROn); !errors.Is(err, ErrNoComPort) {
		t.Errorf("SetControl on a plain Telnet peer: %v", err)
	}
	if _, err := c.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "data", func() bool { _, d, _, _ := p.snapshot(); return string(d) == "hi" })
	_, _, _, subs := p.snapshot()
	if len(subs) != 0 {
		t.Errorf("settings sent to a peer that refused: % x", subs)
	}
}

func TestSilentPeerTimesOut(t *testing.T) {
	c, _ := newPair(t, Options{NegotiateTimeout: 30 * time.Millisecond}, nil)
	if err := c.Negotiate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, reason := c.ComPort(); ok || reason == "" {
		t.Fatalf("ComPort = %v, %q", ok, reason)
	}
}

func TestUnsupportedOptionsAreRefusedOnce(t *testing.T) {
	c, p := newPair(t, Options{}, ser2netLike)
	if err := c.Negotiate(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.send(IAC, WILL, 1, IAC, DO, 24)
	eventually(t, "refusals", func() bool {
		_, _, v, _ := p.snapshot()
		return slices.Contains(v, [2]byte{DONT, 1}) && slices.Contains(v, [2]byte{WONT, 24})
	})
	// The peer turning BINARY off is acknowledged once.
	p.send(IAC, DONT, OptBinary, IAC, DONT, OptBinary)
	eventually(t, "WONT BINARY", func() bool { _, _, v, _ := p.snapshot(); return slices.Contains(v, [2]byte{WONT, OptBinary}) })
	time.Sleep(20 * time.Millisecond)
	_, _, verbs, _ := p.snapshot()
	n := 0
	for _, v := range verbs {
		if v == [2]byte{WONT, OptBinary} {
			n++
		}
	}
	if n != 1 {
		t.Errorf("WONT BINARY sent %d times", n)
	}
	// Without BINARY a lone CR goes out as CR NUL.
	if _, err := c.Write([]byte("a\rb\r\n")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "CR NUL", func() bool { r, _, _, _ := p.snapshot(); return bytes.HasSuffix(r, []byte("a\r\x00b\r\n")) })
}

func TestReadEndsWhenTheConnectionCloses(t *testing.T) {
	c, p := newPair(t, Options{}, nil)
	p.conn.Close()
	if _, err := c.Read(make([]byte, 8)); err == nil {
		t.Fatal("read after close succeeded")
	}
	if ok, _ := c.ComPort(); ok {
		t.Fatal("closed connection has COM-PORT-OPTION")
	}
}

func TestNVTDropsTheNULAfterCR(t *testing.T) {
	ps := &parser{}
	got := ps.feed([]byte("a\r"), nil)
	got = ps.feed([]byte("\x00b\x00"), got)
	if string(got) != "a\rb\x00" {
		t.Fatalf("%q", got)
	}
}

func FuzzParser(f *testing.F) {
	f.Add([]byte("plain"), uint8(3))
	f.Add([]byte{IAC, IAC, 'a', IAC, SB, OptComPort, 107, 0x80, IAC, SE, IAC, WILL}, uint8(1))
	f.Add([]byte{IAC, SB, IAC, WILL, 1, IAC, SB, 1, IAC, IAC, IAC, SE, '\r', 0}, uint8(5))
	f.Fuzz(func(t *testing.T, in []byte, cut uint8) {
		var verbs1, verbs2 [][2]byte
		var subs1, subs2 []string
		whole := &parser{
			onVerb: func(v, o byte) { verbs1 = append(verbs1, [2]byte{v, o}) },
			onSub:  func(b []byte) { subs1 = append(subs1, string(b)) },
		}
		out1 := whole.feed(in, nil)

		// Any split into reads gives the same result.
		split := &parser{
			onVerb: func(v, o byte) { verbs2 = append(verbs2, [2]byte{v, o}) },
			onSub:  func(b []byte) { subs2 = append(subs2, string(b)) },
		}
		step := int(cut)%7 + 1
		var out2 []byte
		for i := 0; i < len(in); i += step {
			out2 = split.feed(in[i:min(i+step, len(in))], out2)
		}
		if !bytes.Equal(out1, out2) || !slices.Equal(verbs1, verbs2) || !slices.Equal(subs1, subs2) {
			t.Fatalf("split reads differ: % x vs % x", out1, out2)
		}
		if len(out1) > len(in) {
			t.Fatalf("more data out than in")
		}
		for _, s := range subs1 {
			if len(s) > maxSub {
				t.Fatalf("subnegotiation of %d bytes", len(s))
			}
		}

		// Escaped data parses back to itself, in binary mode and in NVT.
		for _, bin := range []bool{true, false} {
			back := (&parser{binary: bin}).feed(escape(in, bin), nil)
			if !bytes.Equal(back, in) {
				t.Fatalf("binary=%v: escape round trip % x -> % x", bin, in, back)
			}
		}
	})
}
