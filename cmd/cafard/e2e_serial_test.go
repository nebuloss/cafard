package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/ingest/serial"
	"github.com/nebuloss/cafard/internal/ingest/serial/rfc2217"
	"github.com/nebuloss/cafard/internal/ingest/serial/rfc2217/rfc2217test"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/testutil"
)

// eventually is testutil.Eventually with the timeout of the cmd tests.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	testutil.Eventually(t, what, 10*time.Second, cond)
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
	dut1 := &rfc2217test.Peer{}
	dut3 := &rfc2217test.Peer{Raw: true}
	ro := &rfc2217test.Peer{Raw: true}
	dut1Addr, dut3Addr, roAddr := dut1.Listen(t), dut3.Listen(t), ro.Listen(t)
	dead := freePort(t, "tcp")
	s := startServer(t, func(c *config.Config) {
		c.Serial.Ports = []serial.Port{
			{Name: "dut1", RFC2217: dut1Addr, Writable: true},
			{Name: "dut3", TCP: dut3Addr, Writable: true},
			{Name: "ro", TCP: roAddr},
			{Name: "dead", RFC2217: dead, Writable: true},
		}
	})
	// The old serial list became sinks of the project imported, once.
	if !strings.Contains(s.logs.String(), "the serial list of the config is imported") {
		t.Errorf("no warning about the serial list:\n%s", s.logs.String())
	}
	driver := []auth.Scope{auth.ScopeRead, auth.ScopeMessage, auth.ScopeSerialWrite}
	tokA := s.tokenOnly("agent-a", auth.KindAgent, driver...)
	tokB := s.tokenOnly("agent-b", auth.KindAgent, driver...)
	tokC := s.tokenOnly("agent-c", auth.KindAgent, auth.ScopeRead, auth.ScopeMessage)
	for _, name := range []string{"agent-a", "agent-b", "agent-c"} {
		s.grant(name, projects.Imported, driver...)
	}
	a, b, c := s.mcp(tokA), s.mcp(tokB), s.mcp(tokC)

	if names := toolNames(t, a); !slices.Contains(names, "serial_write") || !slices.Contains(names, "serial_control") {
		t.Errorf("a driver lacks the serial tools: %v", names)
	}
	if names := toolNames(t, c); slices.Contains(names, "serial_write") || slices.Contains(names, "serial_control") {
		t.Errorf("tools offered without serial_write: %v", names)
	}

	for _, st := range []string{"serial:imported/dut1", "serial:imported/dut3", "serial:imported/ro", "serial:imported/dead"} {
		s.waitEntries(st, 1) // connected, or cannot open
	}
	call(t, a, "create_channel", map[string]any{"name": "bench", "project": projects.Imported, "streams": []string{"serial:imported/dut1"}, "surface": "markers"})

	// Without a claim nobody writes.
	for _, cs := range []*mcp.ClientSession{a, b} {
		text := callFails(t, cs, "serial_write", map[string]any{"stream": "serial:imported/dut1", "data": "reboot"}, "forbidden")
		if !strings.Contains(text, "Claim serial:imported/dut1 first") {
			t.Errorf("unclaimed sentence = %q", text)
		}
		callFails(t, cs, "serial_control", map[string]any{"stream": "serial:imported/dut1", "dtr": false}, "forbidden")
	}

	// The holder writes and drives the lines; the far end sees both.
	call(t, a, "claim", map[string]any{"resource": "serial:imported/dut1", "note": "reset test"})
	_, out := call(t, a, "serial_write", map[string]any{"stream": "serial:imported/dut1", "data": "reboot"})
	if num(out["bytes_written"]) != 7 || num(out["marker_seq"]) == 0 {
		t.Errorf("serial_write = %v", out)
	}
	eventually(t, "the bytes at the far end", func() bool { return bytes.Equal(dut1.Data(), []byte("reboot\r")) })
	_, out = call(t, a, "serial_control", map[string]any{"stream": "serial:imported/dut1", "rts": false, "pulse": map[string]any{"line": "dtr", "ms": 50}})
	if lines := out["lines"].(map[string]any); lines["dtr"] != true || lines["rts"] != false {
		t.Errorf("serial_control = %v", out)
	}
	if ctl := dut1.Control(); !bytes.Equal(ctl, []byte{rfc2217.ControlRTSOff, rfc2217.ControlDTROff, rfc2217.ControlDTROn}) {
		t.Errorf("SET-CONTROL values %v", ctl)
	}
	call(t, a, "serial_control", map[string]any{"stream": "serial:imported/dut1", "baud": 57600})
	if baud := dut1.Baud(); !bytes.Equal(baud, []byte{0, 0, 0xE1, 0}) {
		t.Errorf("SET-BAUDRATE % x", baud)
	}

	// The other agent is refused while the claim holds, by name.
	text := callFails(t, b, "serial_write", map[string]any{"stream": "serial:imported/dut1", "data": "halt"}, "conflict")
	if !strings.Contains(text, "agent-a") {
		t.Errorf("conflict sentence = %q", text)
	}
	callFails(t, b, "serial_control", map[string]any{"stream": "serial:imported/dut1", "pulse": map[string]any{"line": "rts", "ms": 10}}, "conflict")

	// After the release it may claim and write, until its claim expires.
	call(t, a, "release", map[string]any{"resource": "serial:imported/dut1"})
	call(t, b, "claim", map[string]any{"resource": "serial:imported/dut1", "ttl_s": 1})
	call(t, b, "serial_write", map[string]any{"stream": "serial:imported/dut1", "data": "status"})
	callFails(t, a, "serial_write", map[string]any{"stream": "serial:imported/dut1", "data": "x"}, "conflict")
	time.Sleep(1200 * time.Millisecond)
	callFails(t, b, "serial_write", map[string]any{"stream": "serial:imported/dut1", "data": "late"}, "forbidden")
	call(t, a, "claim", map[string]any{"resource": "serial:imported/dut1"})
	eventually(t, "the second write", func() bool {
		return string(dut1.Data()) == "reboot\rstatus\r"
	})
	if ctl := dut1.Control(); len(ctl) != 3 {
		t.Errorf("refused calls reached the port: %v", ctl)
	}

	// Every step is in the stream, and in the channel bound to it.
	want := []string{
		"agent-a claimed serial:imported/dut1",
		`agent-a> reboot\r`,
		"serial: agent-a set RTS off, pulsed DTR for 50 ms",
		"serial: agent-a set 57600 baud",
		"agent-a released serial:imported/dut1",
		"agent-b claimed serial:imported/dut1 for 1s",
		`agent-b> status\r`,
		"claim of agent-b on serial:imported/dut1 expired",
	}
	text, _ = call(t, a, "read", map[string]any{"streams": []string{"serial:imported/dut1"}, "since_seq": 0})
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
	call(t, a, "claim", map[string]any{"resource": "serial:imported/dut3"})
	call(t, a, "serial_write", map[string]any{"stream": "serial:imported/dut3", "data": "ls", "newline": "\n"})
	eventually(t, "raw tcp data", func() bool { return string(dut3.Data()) == "ls\n" })
	text = callFails(t, a, "serial_control", map[string]any{"stream": "serial:imported/dut3", "dtr": true}, "bad_request")
	if !strings.Contains(text, "raw TCP carries data only") {
		t.Errorf("raw tcp sentence = %q", text)
	}

	// A port that is not writable is refused even to a claim holder.
	call(t, a, "claim", map[string]any{"resource": "serial:imported/ro"})
	text = callFails(t, a, "serial_write", map[string]any{"stream": "serial:imported/ro", "data": "x"}, "forbidden")
	if !strings.Contains(text, "port of sink imported/ro writable") {
		t.Errorf("read-only sentence = %q", text)
	}
	if d := ro.Data(); len(d) != 0 {
		t.Errorf("read-only port got %q", d)
	}

	// A port that is down is busy, with a hint to retry.
	call(t, a, "claim", map[string]any{"resource": "serial:imported/dead"})
	res, text, out := callResult(t, a, "serial_write", map[string]any{"stream": "serial:imported/dead", "data": "x"})
	if !res.IsError || out["code"] != "busy" || num(out["retry_after_s"]) == 0 || !strings.Contains(text, "try again") {
		t.Errorf("down port = %q %v", text, out)
	}
	text = callFails(t, a, "serial_write", map[string]any{"stream": "serial:imported/nope", "data": "x"}, "not_found")
	if !strings.Contains(text, "serial:imported/dut1") {
		t.Errorf("unknown stream sentence = %q", text)
	}
}
