package web_test

import (
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

func TestLiveShowsMessagesOnlyToTheirViewers(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	admin := e.Admin()
	e.Principal("bob", auth.KindHuman, auth.ScopeRead)
	bob := e.Session("bob", auth.ScopeRead)
	get := func(path string, c any) string {
		t.Helper()
		rec := e.Do("GET", path, admin, nil)
		if c != nil {
			rec = e.Do("GET", path, bob, nil)
		}
		if rec.Code != 200 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		return rec.Body.String()
	}
	body := get("/live?kinds=line&kinds=marker&kinds=message", nil)
	for _, want := range []string{"U-Boot 2024.04", `data-kind="message"`, "codex-build: image v42 ready", "codex-build: Build 812 is green.", ">@codex-build<", ">#dut1<"} {
		if !strings.Contains(body, want) {
			t.Errorf("admin, all kinds: missing %q", want)
		}
	}
	if !strings.Contains(get("/live", nil), "U-Boot") || strings.Contains(get("/live", nil), `data-kind="message"`) {
		t.Error("without kinds=message, /live shows no message")
	}
	body = get("/live?kinds=message", "bob")
	if !strings.Contains(body, "image v42 ready") || strings.Contains(body, "Build 812") || strings.Contains(body, "U-Boot") {
		t.Errorf("bob, messages only: a direct message to admin leaked, or lines came along")
	}
	body = get("/live?channel=dut1&kinds=line,marker,message", nil)
	if !strings.Contains(body, "U-Boot") || !strings.Contains(body, "image v42 ready") || strings.Contains(body, "Build 812") || strings.Contains(body, "Started cafard push") {
		t.Errorf("channel=dut1: want its lines and messages only")
	}
	if !strings.Contains(body, `<option value="dut1" selected>#dut1</option>`) || !strings.Contains(body, `value="message" checked`) {
		t.Errorf("the filter form does not show channel=dut1 and kinds=message")
	}
}

func TestLiveCarriesLineActionsAndTheQuickDrop(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	body := e.Do("GET", "/live", e.Admin(), nil).Body.String()
	for _, want := range []string{`data-component="LivePane"`, `data-component="RangeBar"`, `data-action="attach-line"`, `data-action="attach-from"`,
		`data-action="select"`, `data-component="QuickDropSheet"`, `<option value="chan:dut1">#dut1</option>`, `<select name="channel">`} {
		if !strings.Contains(body, want) {
			t.Errorf("/live: missing %q", want)
		}
	}
	e.Principal("reader", auth.KindHuman, auth.ScopeRead)
	body = e.Do("GET", "/live", e.Session("reader", auth.ScopeRead), nil).Body.String()
	if strings.Contains(body, `data-component="QuickDropSheet"`) {
		t.Error("a session without the message scope gets the quick drop")
	}
}

func TestSourcesShowEverySourceField(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	body := e.Do("GET", "/streams", e.Admin(), nil).Body.String()
	for _, want := range []string{`data-stream="serial:dut1" data-status="connected"`, `class="stream-dot"`, "serial / rfc2217",
		`data-slot="rate"`, `data-slot="last"`, "systemd[1]: Started cafard push.", `data-slot="dropped">0<`, "Open in Live"} {
		if !strings.Contains(body, want) {
			t.Errorf("/streams: missing %q", want)
		}
	}
}
