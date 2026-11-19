package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/testutil"
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

// listening waits until the server logs that component listens on
// transport, and returns the bound address. Tests configure port 0, so no
// other process can take the port between choosing it and binding it, and
// nothing probes the port while the server binds it.
func (s *server) listening(component, transport string) string {
	s.t.Helper()
	re := regexp.MustCompile(`msg=listening component=` + component + ` transport=` + transport + ` addr=(\S+)`)
	var addr string
	testutil.Eventually(s.t, component+" "+transport+" listening", 10*time.Second, func() bool {
		if m := re.FindStringSubmatch(s.logs.String()); m != nil {
			addr = m[1]
		}
		return addr != ""
	})
	return addr
}

// waitEntries polls the store until stream has at least n entries.
func (s *server) waitEntries(stream string, n int) []store.Entry {
	s.t.Helper()
	var entries []store.Entry
	testutil.Eventually(s.t, fmt.Sprintf("%d entries in %s", n, stream), 10*time.Second, func() bool {
		st, err := store.Open(context.Background(), filepath.Join(s.dir, store.FileName))
		if err != nil {
			s.t.Fatal(err)
		}
		res, err := st.Read(context.Background(), store.Query{Streams: []string{stream}, Limit: 1000})
		st.Close()
		if err != nil {
			s.t.Fatal(err)
		}
		entries = res.Entries
		return len(entries) >= n
	})
	return entries
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
	s := startServerWith(t, append(slices.Clone(testSinks), "local=127.0.0.1"), func(c *config.Config) { c.Syslog.UDP, c.Syslog.TCP = "127.0.0.1:0", "127.0.0.1:0" })
	udpAddr, tcpAddr := s.listening("syslog", "udp"), s.listening("syslog", "tcp")

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

	// Both hosts send from 127.0.0.1, the address of the sink local.
	all := s.waitEntries("syslog:lab/local", 3)
	byHost := map[string][]store.Entry{}
	for _, e := range all {
		byHost[e.Host] = append(byHost[e.Host], e)
	}
	if len(byHost["dut1"]) != 1 || len(byHost["dut2"]) != 2 {
		t.Fatalf("entries by host = %v", byHost)
	}
	checkEntry(t, byHost["dut1"][0], "hello over udp", 3, time.Date(2026, 11, 16, 10, 0, 0, 123e6, time.UTC))
	got := byHost["dut2"]
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

// netconsole.names is imported once into a sink of the project imported,
// with a warning to take it out of the file.
func TestNetconsoleLandsInTheSinkItsOldNameBecame(t *testing.T) {
	s := startServer(t, func(c *config.Config) {
		c.Netconsole.UDP = "127.0.0.1:0"
		c.Netconsole.Names = map[string]string{"127.0.0.1": "vm1"}
	})
	addr := s.listening("netconsole", "udp")
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
	got := s.waitEntries("netconsole:imported/vm1", 2)
	if !strings.Contains(s.logs.String(), "netconsole.names in the config is imported") {
		t.Errorf("no warning about netconsole.names:\n%s", s.logs.String())
	}
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
	got := s.waitEntries("otlp:lab/checkout", 1)
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
	// Without a grant on lab, the record reaches no sink and is rejected.
	resp = s.post(s.tokenOnly("stranger", auth.KindDevice, auth.ScopeIngest), "/v1/logs", "application/json", body)
	b, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"rejectedLogRecords":"1"`) || !strings.Contains(string(b), "no sink") {
		t.Errorf("unrouted = %d %s", resp.StatusCode, b)
	}
}
