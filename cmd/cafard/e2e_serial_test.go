package main

import (
	"bytes"
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/ingest/serial"
	"github.com/nebuloss/cafard/internal/ingest/serial/rfc2217"
)

// fakeSer2net is an RFC 2217 server (or, with raw, a plain TCP serial
// server) on a loopback port. It accepts every option, answers every
// COM-PORT-OPTION command, and records the data and SET-CONTROL values
// it receives.
type fakeSer2net struct {
	ln  net.Listener
	raw bool

	mu      sync.Mutex
	data    []byte
	control []byte
	baud    []byte
}

func newFakeSer2net(t *testing.T, raw bool) *fakeSer2net {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSer2net{ln: ln, raw: raw}
	var conns []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go f.serve(c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	return f
}

func (f *fakeSer2net) addr() string { return f.ln.Addr().String() }

func (f *fakeSer2net) serve(c net.Conn) {
	var (
		sub   []byte
		verb  byte
		state int // 0 data, 1 IAC, 2 verb, 3 SB, 4 SB IAC
	)
	buf := make([]byte, 512)
	for {
		n, err := c.Read(buf)
		f.mu.Lock()
		for _, b := range buf[:n] {
			if f.raw {
				f.data = append(f.data, b)
				continue
			}
			switch state {
			case 0:
				if b == rfc2217.IAC {
					state = 1
				} else {
					f.data = append(f.data, b)
				}
			case 1:
				switch b {
				case rfc2217.IAC:
					f.data, state = append(f.data, b), 0
				case rfc2217.WILL, rfc2217.DO, rfc2217.WONT, rfc2217.DONT:
					verb, state = b, 2
				case rfc2217.SB:
					sub, state = nil, 3
				default:
					state = 0
				}
			case 2:
				state = 0
				// TCP buffers, so answering inline cannot block both ends.
				switch verb {
				case rfc2217.WILL:
					c.Write([]byte{rfc2217.IAC, rfc2217.DO, b})
				case rfc2217.DO:
					c.Write([]byte{rfc2217.IAC, rfc2217.WILL, b})
				}
			case 3:
				if b == rfc2217.IAC {
					state = 4
				} else {
					sub = append(sub, b)
				}
			case 4:
				if b != rfc2217.SE {
					sub, state = append(sub, b), 3
					continue
				}
				state = 0
				if len(sub) < 2 {
					continue
				}
				switch sub[1] {
				case rfc2217.CmdSetControl:
					f.control = append(f.control, sub[2:]...)
				case rfc2217.CmdSetBaudRate:
					f.baud = slices.Clone(sub[2:])
				}
				out := append([]byte{rfc2217.IAC, rfc2217.SB, sub[0], sub[1] + rfc2217.ServerOffset}, sub[2:]...)
				c.Write(append(out, rfc2217.IAC, rfc2217.SE))
			}
		}
		f.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (f *fakeSer2net) received() (data, control, baud []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.data), slices.Clone(f.control), slices.Clone(f.baud)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestTwoAgentsContendForASerialPort(t *testing.T) {
	dut1 := newFakeSer2net(t, false)
	dut3 := newFakeSer2net(t, true)
	ro := newFakeSer2net(t, true)
	dead := freePort(t, "tcp")
	s := startServer(t, func(c *config.Config) {
		c.Serial.Ports = []serial.Port{
			{Name: "dut1", RFC2217: dut1.addr(), Writable: true},
			{Name: "dut3", TCP: dut3.addr(), Writable: true},
			{Name: "ro", TCP: ro.addr()},
			{Name: "dead", RFC2217: dead, Writable: true},
		}
	})
	driver := []auth.Scope{auth.ScopeRead, auth.ScopeMessage, auth.ScopeSerialWrite}
	a := s.mcp(s.token("agent-a", auth.KindAgent, driver...))
	b := s.mcp(s.token("agent-b", auth.KindAgent, driver...))
	_, c := s.agent("agent-c") // read and message only

	if names := toolNames(t, a); !slices.Contains(names, "serial_write") || !slices.Contains(names, "serial_control") {
		t.Errorf("a driver lacks the serial tools: %v", names)
	}
	if names := toolNames(t, c); slices.Contains(names, "serial_write") || slices.Contains(names, "serial_control") {
		t.Errorf("tools offered without serial_write: %v", names)
	}

	for _, st := range []string{"serial:dut1", "serial:dut3", "serial:ro", "serial:dead"} {
		s.waitEntries(st, 1) // connected, or cannot open
	}
	call(t, a, "create_channel", map[string]any{"name": "bench", "streams": []string{"serial:dut1"}, "surface": "markers"})

	// Without a claim nobody writes.
	for _, cs := range []*mcp.ClientSession{a, b} {
		text := callFails(t, cs, "serial_write", map[string]any{"stream": "serial:dut1", "data": "reboot"}, "forbidden")
		if !strings.Contains(text, "Claim serial:dut1 first") {
			t.Errorf("unclaimed sentence = %q", text)
		}
		callFails(t, cs, "serial_control", map[string]any{"stream": "serial:dut1", "dtr": false}, "forbidden")
	}

	// The holder writes and drives the lines; the far end sees both.
	call(t, a, "claim", map[string]any{"resource": "serial:dut1", "note": "reset test"})
	_, out := call(t, a, "serial_write", map[string]any{"stream": "serial:dut1", "data": "reboot"})
	if num(out["bytes_written"]) != 7 || num(out["marker_seq"]) == 0 {
		t.Errorf("serial_write = %v", out)
	}
	eventually(t, "the bytes at the far end", func() bool { d, _, _ := dut1.received(); return bytes.Equal(d, []byte("reboot\r")) })
	_, out = call(t, a, "serial_control", map[string]any{"stream": "serial:dut1", "rts": false, "pulse": map[string]any{"line": "dtr", "ms": 50}})
	if lines := out["lines"].(map[string]any); lines["dtr"] != true || lines["rts"] != false {
		t.Errorf("serial_control = %v", out)
	}
	if _, ctl, _ := dut1.received(); !bytes.Equal(ctl, []byte{rfc2217.ControlRTSOff, rfc2217.ControlDTROff, rfc2217.ControlDTROn}) {
		t.Errorf("SET-CONTROL values %v", ctl)
	}
	call(t, a, "serial_control", map[string]any{"stream": "serial:dut1", "baud": 57600})
	if _, _, baud := dut1.received(); !bytes.Equal(baud, []byte{0, 0, 0xE1, 0}) {
		t.Errorf("SET-BAUDRATE % x", baud)
	}

	// The other agent is refused while the claim holds, by name.
	text := callFails(t, b, "serial_write", map[string]any{"stream": "serial:dut1", "data": "halt"}, "conflict")
	if !strings.Contains(text, "agent-a") {
		t.Errorf("conflict sentence = %q", text)
	}
	callFails(t, b, "serial_control", map[string]any{"stream": "serial:dut1", "pulse": map[string]any{"line": "rts", "ms": 10}}, "conflict")

	// After the release it may claim and write, until its claim expires.
	call(t, a, "release", map[string]any{"resource": "serial:dut1"})
	call(t, b, "claim", map[string]any{"resource": "serial:dut1", "ttl_s": 1})
	call(t, b, "serial_write", map[string]any{"stream": "serial:dut1", "data": "status"})
	callFails(t, a, "serial_write", map[string]any{"stream": "serial:dut1", "data": "x"}, "conflict")
	time.Sleep(1200 * time.Millisecond)
	callFails(t, b, "serial_write", map[string]any{"stream": "serial:dut1", "data": "late"}, "forbidden")
	call(t, a, "claim", map[string]any{"resource": "serial:dut1"})
	eventually(t, "the second write", func() bool {
		d, _, _ := dut1.received()
		return string(d) == "reboot\rstatus\r"
	})
	if _, ctl, _ := dut1.received(); len(ctl) != 3 {
		t.Errorf("refused calls reached the port: %v", ctl)
	}

	// Every step is in the stream, and in the channel bound to it.
	want := []string{
		"agent-a claimed serial:dut1",
		`agent-a> reboot\r`,
		"serial: agent-a set RTS off, pulsed DTR for 50 ms",
		"serial: agent-a set 57600 baud",
		"agent-a released serial:dut1",
		"agent-b claimed serial:dut1 for 1s",
		`agent-b> status\r`,
		"claim of agent-b on serial:dut1 expired",
	}
	text, _ = call(t, a, "read", map[string]any{"streams": []string{"serial:dut1"}, "since_seq": 0})
	last := -1
	for _, w := range want {
		i := strings.Index(text, w)
		if i < 0 || i < last {
			t.Errorf("stream lacks %q in order:\n%s", w, text)
		}
		last = i
	}
	if strings.Contains(text, "halt") || strings.Contains(text, "late") {
		t.Errorf("a refused write left a marker:\n%s", text)
	}
	timeline, _ := call(t, a, "channel_timeline", map[string]any{"channel": "bench", "limit": 100})
	for _, w := range want[1:4] {
		if !strings.Contains(timeline, w) {
			t.Errorf("channel timeline lacks %q:\n%s", w, timeline)
		}
	}

	// A raw TCP port takes data but not line control, and says why.
	call(t, a, "claim", map[string]any{"resource": "serial:dut3"})
	call(t, a, "serial_write", map[string]any{"stream": "serial:dut3", "data": "ls", "newline": "\n"})
	eventually(t, "raw tcp data", func() bool { d, _, _ := dut3.received(); return string(d) == "ls\n" })
	text = callFails(t, a, "serial_control", map[string]any{"stream": "serial:dut3", "dtr": true}, "bad_request")
	if !strings.Contains(text, "raw TCP carries data only") {
		t.Errorf("raw tcp sentence = %q", text)
	}

	// A port that is not writable is refused even to a claim holder.
	call(t, a, "claim", map[string]any{"resource": "serial:ro"})
	text = callFails(t, a, "serial_write", map[string]any{"stream": "serial:ro", "data": "x"}, "forbidden")
	if !strings.Contains(text, "writable: true") {
		t.Errorf("read-only sentence = %q", text)
	}
	if d, _, _ := ro.received(); len(d) != 0 {
		t.Errorf("read-only port got %q", d)
	}

	// A port that is down is busy, with a hint to retry.
	call(t, a, "claim", map[string]any{"resource": "serial:dead"})
	res, text, out := callResult(t, a, "serial_write", map[string]any{"stream": "serial:dead", "data": "x"})
	if !res.IsError || out["code"] != "busy" || num(out["retry_after_s"]) == 0 || !strings.Contains(text, "try again") {
		t.Errorf("down port = %q %v", text, out)
	}
	text = callFails(t, a, "serial_write", map[string]any{"stream": "serial:nope", "data": "x"}, "not_found")
	if !strings.Contains(text, "serial:dut1") {
		t.Errorf("unknown stream sentence = %q", text)
	}
}
