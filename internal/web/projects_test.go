package web_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

// The admin makes a project and runs it from its page with plain form
// posts: channels, sinks with their addresses, port and tokens,
// retention, members and sign-in links.
func TestTheAdminRunsAProjectFromItsPage(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	admin := e.Admin()
	ctx := context.Background()
	post := func(path string, form url.Values, want int) string {
		t.Helper()
		rec := e.Do("POST", path, admin, form)
		if rec.Code != want {
			t.Fatalf("%s: %d, want %d\n%s", path, rec.Code, want, rec.Body)
		}
		return rec.Body.String()
	}

	post("/projects", url.Values{"name": {"Bad Name"}}, http.StatusBadRequest)
	rec := e.Do("POST", "/projects", admin, url.Values{"name": {"rack"}, "title": {"Rack 2"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/projects/rack" {
		t.Fatalf("create: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if body := e.Do("GET", "/projects", admin, nil).Body.String(); !strings.Contains(body, `href="/projects/rack"`) || !strings.Contains(body, "Rack 2") {
		t.Error("the list lacks the new project")
	}
	page := e.Do("GET", "/projects/rack", admin, nil).Body.String()
	if !strings.Contains(page, "#general") {
		t.Error("a new project has no #general")
	}

	post("/projects/rack/channels", url.Values{"name": {"dut9"}, "patterns": {"serial:rack/dut9"}}, http.StatusSeeOther)
	post("/projects/rack/channels", url.Values{"name": {"x"}, "patterns": {"serial:lab/dut1"}}, http.StatusBadRequest)
	post("/projects/rack/sinks", url.Values{"name": {"dut9"}, "sources": {"10.0.50.42\n10.0.51.0/24"},
		"serial_kind": {"rfc2217"}, "serial_address": {"10.0.50.30:7001"}, "baud": {"57600"}, "writable": {"1"}}, http.StatusSeeOther)
	post("/projects/rack/sinks", url.Values{"name": {"bad"}, "sources": {"not-an-ip"}}, http.StatusBadRequest)
	post("/projects/rack/sinks", url.Values{"name": {"bad"}, "serial_kind": {"tcp"}}, http.StatusBadRequest)
	k, err := e.Deps.Projects.Sink(ctx, "rack", "dut9")
	if err != nil || len(k.Sources) != 2 || k.Serial == nil || k.Serial.RFC2217 != "10.0.50.30:7001" || k.Serial.Baud != 57600 || !k.Serial.Writable {
		t.Fatalf("sink = %+v %v", k, err)
	}
	page = e.Do("GET", "/projects/rack", admin, nil).Body.String()
	for _, want := range []string{"10.0.50.42, 10.0.51.0/24", "rfc2217 10.0.50.30:7001, 57600 8N1, writable", `action="/projects/rack/sinks/dut9"`} {
		if !strings.Contains(page, want) {
			t.Errorf("project page lacks %q", want)
		}
	}
	// Saving the sink again without a port removes it.
	post("/projects/rack/sinks/dut9", url.Values{"sources": {"10.0.50.42"}, "serial_kind": {"none"}}, http.StatusSeeOther)
	if k, _ = e.Deps.Projects.Sink(ctx, "rack", "dut9"); k.Serial != nil || len(k.Sources) != 1 {
		t.Errorf("edited sink = %+v", k)
	}

	body := post("/projects/rack/sinks/dut9/tokens", url.Values{"label": {"bench pi"}}, http.StatusCreated)
	secret := regexp.MustCompile(`cfd_[A-Za-z0-9_-]+`).FindString(body)
	p, err := e.Tokens.Lookup(ctx, secret)
	if err != nil || p.Sink != "rack/dut9" || p.Name != "rack-dut9" || p.Kind != auth.KindDevice {
		t.Errorf("sink token = %+v %v", p, err)
	}
	if !strings.Contains(e.Do("GET", "/projects/rack", admin, nil).Body.String(), "bench pi") {
		t.Error("the sink token is not listed")
	}

	post("/projects/rack/retention", url.Values{"lines_max_age": {"720h"}, "lines_max_bytes": {"1GB"}, "messages_max_age": {"0"}, "blobs_max_bytes": {"512MiB"}}, http.StatusSeeOther)
	post("/projects/rack/retention", url.Values{"lines_max_age": {"forever"}, "lines_max_bytes": {"1GB"}, "messages_max_age": {"0"}, "blobs_max_bytes": {"0"}}, http.StatusBadRequest)
	if pr, _ := e.Deps.Projects.Project(ctx, "rack"); pr.Retention.LinesMaxAge.Hours() != 720 || pr.Retention.LinesMaxBytes != 1e9 || pr.Retention.BlobsMaxBytes != 512<<20 {
		t.Errorf("retention = %+v", pr.Retention)
	}
	if page := e.Do("GET", "/projects/rack", admin, nil).Body.String(); !strings.Contains(page, `value="720h"`) || !strings.Contains(page, `value="512MiB"`) {
		t.Error("the retention form does not show the saved values")
	}

	post("/projects/rack/grants", url.Values{"principal": {"codex"}, "kind": {"agent"}, "channel": {"dut9"}, "scopes": {"read", "message"}}, http.StatusSeeOther)
	post("/projects/rack/grants", url.Values{"principal": {"codex"}, "channel": {"dut9"}, "scopes": {"ingest"}}, http.StatusBadRequest)
	gs, err := e.Deps.Projects.Grants(ctx, "rack", "codex")
	if err != nil || len(gs) != 1 || gs[0].Channel != "dut9" || gs[0].Scopes.String() != "read message" {
		t.Fatalf("grants = %+v %v", gs, err)
	}
	post("/projects/rack/grants/"+webtest.Itoa(gs[0].ID)+"/delete", url.Values{}, http.StatusSeeOther)
	if gs, _ = e.Deps.Projects.Grants(ctx, "rack", "codex"); len(gs) != 0 {
		t.Errorf("grant not removed: %+v", gs)
	}

	body = post("/projects/rack/pairings", url.Values{"new_name": {"alice-phone"}, "channel": {"dut9"}, "scopes": {"read"}}, http.StatusCreated)
	if !strings.Contains(body, "rack:dut9=read") || !regexp.MustCompile(`src="/pair/\d+/qr\.svg"`).MatchString(body) {
		t.Error("the sign-in link of the project is not shown with its grant and QR")
	}

	post("/projects/rack/sinks/dut9/delete", url.Values{}, http.StatusSeeOther)
	if _, err := e.Tokens.Lookup(ctx, secret); err == nil {
		t.Error("deleting the sink left its token working")
	}
	if rec := e.Do("GET", "/projects/nope", admin, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d", rec.Code)
	}
	// Only the admin interface has these pages.
	if rec := e.DoPublic("GET", "/projects", admin, nil); rec.Code != http.StatusNotFound && rec.Code != http.StatusSeeOther {
		t.Errorf("/projects on the public interface: %d", rec.Code)
	}
}

// Chats, Sources and Live group what they list by project.
func TestListsAreGroupedByProject(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	if _, err := e.Deps.Projects.SaveSink(context.Background(), projects.Sink{Project: "lab", Name: "bench-pi"}); err != nil {
		t.Fatal(err)
	}
	admin := e.Admin()
	home := e.Do("GET", "/", admin, nil).Body.String()
	if !regexp.MustCompile(`(?s)<section class="project-chats" data-project="lab".*data-section="channels" data-project="lab".*#?dut1`).MatchString(home) {
		t.Error("home has no section for lab with its channels")
	}
	if !strings.Contains(home, `href="/live?streams=%2A%3Alab%2Fbench-pi"`) {
		t.Error("home lacks the logs of lab's sinks")
	}
	streams := e.Do("GET", "/streams", admin, nil).Body.String()
	if !regexp.MustCompile(`(?s)<div class="stream-group" role="rowgroup" data-project="lab">.*data-stream="serial:lab/dut1"`).MatchString(streams) {
		t.Error("Sources has no group for lab")
	}
	live := e.Do("GET", "/live", admin, nil).Body.String()
	if !strings.Contains(live, `<optgroup label="Lab">`) || !strings.Contains(live, `<option value="*:lab/*">`) {
		t.Error("Live does not offer the channels and the logs of lab by project")
	}
}
