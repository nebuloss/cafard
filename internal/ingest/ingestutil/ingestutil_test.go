package ingestutil

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/store"
)

func TestStreamName(t *testing.T) {
	tests := map[string]string{
		"dut1":                   "dut1",
		" web 1 ":                "web_1",
		"a*b?c[d]\\e":            "a_b_c_d]_e",
		"bad\xffutf8":            "bad_utf8",
		"":                       "",
		strings.Repeat("x", 500): strings.Repeat("x", maxNameBytes),
	}
	for in, want := range tests {
		got := StreamName(in)
		if got != want {
			t.Errorf("StreamName(%q) = %q, want %q", in, got, want)
		}
		if got != "" {
			if err := store.ValidateStreamID("syslog:" + got); err != nil {
				t.Errorf("%q: %v", got, err)
			}
		}
	}
}

func TestAllowlist(t *testing.T) {
	al, err := ParseAllowlist([]string{"10.0.50.0/24", "192.0.2.1", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]bool{
		"10.0.50.9": true, "10.0.51.1": false, "192.0.2.1": true, "192.0.2.2": false,
		"::ffff:10.0.50.3": true, "2001:db8::5": true, "2001:db9::5": false,
	} {
		if got := al.Allows(netip.MustParseAddr(ip)); got != want {
			t.Errorf("Allows(%s) = %v", ip, got)
		}
	}
	if !(Allowlist(nil)).Allows(netip.MustParseAddr("8.8.8.8")) {
		t.Error("an empty list must allow everyone")
	}
	if _, err := ParseAllowlist([]string{"lan"}); err == nil {
		t.Error("a name was accepted")
	}
}

func TestMeta(t *testing.T) {
	var empty Meta
	empty.Add("x", "")
	empty.Add("y", nil)
	empty.Add("z", map[string]any{})
	if empty.JSON() != nil {
		t.Errorf("empty meta %s", empty.JSON())
	}

	var m Meta
	m.Add("trace_id", "abc")
	attrs := map[string]any{}
	for i := range 500 {
		attrs[strings.Repeat("k", 1+i%40)+string(rune('a'+i%26))] = strings.Repeat("v", 30)
	}
	m.Add("attributes", attrs)
	m.Add("resource", attrs)
	m.Add("after", 1)
	out := m.JSON()
	if len(out) > store.MaxMetaBytes || !json.Valid(out) {
		t.Fatalf("meta of %d bytes, valid %v", len(out), json.Valid(out))
	}
	var back map[string]any
	_ = json.Unmarshal(out, &back)
	if back["trace_id"] != "abc" || back["truncated"] != true || back["after"] != float64(1) || len(back["attributes"].(map[string]any)) == 0 {
		t.Errorf("meta %s", out)
	}

	var small Meta
	small.Add("a", 1)
	small.Add("b", map[string]any{"c": "d"})
	if string(small.JSON()) != `{"a":1,"b":{"c":"d"}}` {
		t.Errorf("small %s", small.JSON())
	}
}

func TestSplitLines(t *testing.T) {
	got := SplitLines([]byte("a\r\nb\rc\n\xff"))
	if strings.Join(got, "|") != "a|b|c|\uFFFD" {
		t.Errorf("%q", got)
	}
}
