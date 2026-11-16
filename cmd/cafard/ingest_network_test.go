package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/store"
)

// testConfig is the default configuration without the syslog and
// netconsole listeners, whose default ports are shared by every test run
// on the machine. Tests that need them bind loopback ports of their own.
func testConfig() config.Config {
	cfg := config.Default()
	cfg.Syslog.UDP, cfg.Syslog.TCP, cfg.Netconsole.UDP = "", "", ""
	return cfg
}

// freePort returns a loopback address with a port free for network
// ("udp" or "tcp") right now.
func freePort(t *testing.T, network string) string {
	t.Helper()
	if network == "udp" {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		return pc.LocalAddr().String()
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// waitBound waits until the server has bound addr: listeners start after
// the HTTP server reports ready.
func waitBound(t *testing.T, network, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		if network == "udp" {
			var pc net.PacketConn
			if pc, err = net.ListenPacket("udp", addr); err == nil {
				pc.Close()
			}
		} else {
			var ln net.Listener
			if ln, err = net.Listen("tcp", addr); err == nil {
				ln.Close()
			}
		}
		if err != nil {
			return // in use: the server has it
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s %s never bound", network, addr)
}

// waitEntries polls the store until stream has at least n entries.
func (s *server) waitEntries(stream string, n int) []store.Entry {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := store.Open(context.Background(), filepath.Join(s.dir, store.FileName))
		if err != nil {
			s.t.Fatal(err)
		}
		res, err := st.Read(context.Background(), store.Query{Streams: []string{stream}, Limit: 1000})
		st.Close()
		if err != nil {
			s.t.Fatal(err)
		}
		if len(res.Entries) >= n {
			return res.Entries
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("stream %s has %d entries, want %d", stream, len(res.Entries), n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func checkEntry(t *testing.T, e store.Entry, text string, sev int, src time.Time) {
	t.Helper()
	if e.Text != text || e.Severity != sev {
		t.Errorf("entry = %q severity %d, want %q severity %d", e.Text, e.Severity, text, sev)
	}
	if src.IsZero() {
		return
	}
	if e.SrcTS == nil || !e.SrcTS.Equal(src) {
		t.Errorf("%q: src_ts = %v, want %v", text, e.SrcTS, src)
	}
}

func TestSyslogOverUDPAndTCPLands(t *testing.T) {
	udpAddr, tcpAddr := freePort(t, "udp"), freePort(t, "tcp")
	s := startServer(t, func(c *config.Config) { c.Syslog.UDP, c.Syslog.TCP = udpAddr, tcpAddr })
	waitBound(t, "udp", udpAddr)
	waitBound(t, "tcp", tcpAddr)

	uc, err := net.Dial("udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	if _, err := uc.Write([]byte("<11>1 2026-11-16T10:00:00.123Z dut1 app 42 - - hello over udp\n")); err != nil {
		t.Fatal(err)
	}

	// What `logger -n host -P port -T --octet-count hello` sends.
	tc, err := net.Dial("tcp", tcpAddr)
	if err != nil {
		t.Fatal(err)
	}
	frames := ""
	for _, msg := range []string{
		`<13>1 2026-11-16T11:00:00.5+01:00 dut2 guillaume - - [timeQuality tzKnown="1" isSynced="1"] hello`,
		`<12>Nov 16 10:00:01 dut2 kernel: second line`,
	} {
		frames += fmt.Sprintf("%d %s", len(msg), msg)
	}
	if _, err := tc.Write([]byte(frames)); err != nil {
		t.Fatal(err)
	}
	tc.Close()

	got := s.waitEntries("syslog:dut1", 1)
	checkEntry(t, got[0], "hello over udp", 3, time.Date(2026, 11, 16, 10, 0, 0, 123e6, time.UTC))
	got = s.waitEntries("syslog:dut2", 2)
	checkEntry(t, got[0], "hello", 5, time.Date(2026, 11, 16, 10, 0, 0, 5e8, time.UTC))
	checkEntry(t, got[1], "second line", 4, time.Time{})
	if got[1].App != "kernel" || got[1].SrcTS == nil {
		t.Errorf("RFC 3164 frame: app %q src_ts %v", got[1].App, got[1].SrcTS)
	}
	var meta map[string]any
	if err := json.Unmarshal(got[0].Meta, &meta); err != nil || meta["facility_name"] != "user" {
		t.Errorf("meta = %s, want facility user", got[0].Meta)
	}
}

func TestNetconsoleLandsUnderItsConfiguredName(t *testing.T) {
	addr := freePort(t, "udp")
	s := startServer(t, func(c *config.Config) {
		c.Netconsole.UDP = addr
		c.Netconsole.Names = map[string]string{"127.0.0.1": "vm1"}
	})
	waitBound(t, "udp", addr)
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, d := range []string{
		"3,812,90123456,-;e1000e: eth0 NIC Link is Down\n SUBSYSTEM=net\n DEVICE=n2\n",
		"[   12.345678] plain line\n",
	} {
		if _, err := c.Write([]byte(d)); err != nil {
			t.Fatal(err)
		}
	}
	got := s.waitEntries("netconsole:vm1", 2)
	checkEntry(t, got[0], "e1000e: eth0 NIC Link is Down", 3, time.Time{})
	checkEntry(t, got[1], "[   12.345678] plain line", 6, time.Time{})
	var meta map[string]any
	if err := json.Unmarshal(got[0].Meta, &meta); err != nil || meta["seq"] != float64(812) {
		t.Errorf("meta = %s", got[0].Meta)
	}
}

func TestOTLPJSONLands(t *testing.T) {
	s := startServer(t)
	tok := s.token("collector", auth.KindDevice, auth.ScopeIngest)
	body := `{"resourceLogs":[{"resource":{"attributes":[
		{"key":"service.name","value":{"stringValue":"checkout"}},
		{"key":"host.name","value":{"stringValue":"web1"}}]},
	  "scopeLogs":[{"scope":{"name":"app"},"logRecords":[
		{"timeUnixNano":"1763287200000000000","severityNumber":17,"severityText":"ERROR",
		 "body":{"stringValue":"payment failed"},
		 "traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174",
		 "attributes":[{"key":"order","value":{"intValue":"42"}}]}]}]}]}`
	resp := s.post(tok, "/v1/logs", "application/json", body)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(b) != "{}" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("status %d %q %s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	got := s.waitEntries("otlp:checkout", 1)
	checkEntry(t, got[0], "payment failed", 3, time.Unix(0, 1763287200000000000))
	if got[0].Host != "web1" || got[0].App != "checkout" {
		t.Errorf("host %q app %q", got[0].Host, got[0].App)
	}
	var meta map[string]any
	if err := json.Unmarshal(got[0].Meta, &meta); err != nil || meta["trace_id"] != "5b8efff798038103d269b633813fc60c" {
		t.Errorf("meta = %s", got[0].Meta)
	}

	if resp := s.post("", "/v1/logs", "application/json", body); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", resp.StatusCode)
	}
	if resp := s.post(tok, "/v1/traces", "application/json", "{}"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("/v1/traces: status %d, want 404", resp.StatusCode)
	}
}
