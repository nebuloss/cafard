package web_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

// Links in agents' messages, gallery refs and bookmarks carry the
// addresses chats and galleries had before the project view: every one
// still resolves, by a permanent redirect to its new place.
func TestEveryOldAddressStillResolves(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	seedGallery(e)
	cookie := e.Admin()
	item := galleryShots.items[1]
	moved := map[string]string{
		"/c/lab/dut1":                        "/projects/lab/c/dut1",
		"/c/lab/dut1?view=logs&sev=3":        "/projects/lab/c/dut1?view=logs&sev=3",
		"/c/lab/dut1?f=questions":            "/projects/lab/c/dut1?f=questions",
		"/g/lab":                             "/projects/lab/g",
		"/g/lab/shots":                       "/projects/lab/g/shots",
		"/g/lab/shots?section=login+flow":    "/projects/lab/g/shots?section=login+flow",
		fmt.Sprintf("/g/lab/shots/%d", item): fmt.Sprintf("/projects/lab/g/shots/%d", item),
	}
	for old, want := range moved {
		rec := e.Do("GET", old, cookie, nil)
		if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != want {
			t.Errorf("%s: %d to %q, want 308 to %q", old, rec.Code, rec.Header().Get("Location"), want)
			continue
		}
		if rec := e.Do("GET", want, cookie, nil); rec.Code != http.StatusOK {
			t.Errorf("%s (from %s): %d", want, old, rec.Code)
		}
	}
	// These kept their address.
	for _, path := range []string{"/live", "/live?streams=serial:lab/dut1", "/streams", "/dm/codex-build", "/drop", "/projects"} {
		if rec := e.Do("GET", path, cookie, nil); rec.Code != http.StatusOK {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	// The admin's old project page is the project's view now, with its
	// settings one level down.
	if rec := e.Do("GET", "/projects/lab", cookie, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("/projects/lab: %d", rec.Code)
	}
	if rec := e.Do("GET", "/projects/lab/settings", cookie, nil); rec.Code != http.StatusOK {
		t.Errorf("/projects/lab/settings: %d", rec.Code)
	}
}

func TestAProjectOpensOnItsLatestChat(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	open := func(c *http.Cookie, project string) (int, string) {
		rec := e.Do("GET", "/projects/"+project, c, nil)
		return rec.Code, rec.Header().Get("Location")
	}
	// The question in #dut1 is the newest message of lab.
	if code, loc := open(cookie, "lab"); code != http.StatusSeeOther || loc != "/projects/lab/c/dut1" {
		t.Errorf("lab: %d %s", code, loc)
	}
	// A new project has nothing but #general.
	if _, err := e.Stack.Projects.Create(context.Background(), "rack", "Rack 2"); err != nil {
		t.Fatal(err)
	}
	if code, loc := open(cookie, "rack"); code != http.StatusSeeOther || loc != "/projects/rack/c/general" {
		t.Errorf("rack: %d %s", code, loc)
	}
	// A person with no grant on rack does not learn it exists.
	e.Principal("bob", auth.KindHuman, auth.ScopeRead)
	bob := e.Session("bob", auth.ScopeRead)
	if code, _ := open(bob, "rack"); code != http.StatusNotFound {
		t.Errorf("rack for bob: %d", code)
	}
	if code, loc := open(bob, "lab"); code != http.StatusSeeOther || loc != "/projects/lab/c/dut1" {
		t.Errorf("lab for bob: %d %s", code, loc)
	}
}

// A sink's page shows that sink's streams; the overview every sink's.
func TestAProjectsLogsShowItsSinks(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	one := e.Do("GET", "/projects/lab/logs/dut1", cookie, nil).Body.String()
	if !strings.Contains(one, "U-Boot 2024.04") || strings.Contains(one, "Started cafard push") || strings.Contains(one, `data-stream="syslog:lab/bench-pi"`) {
		t.Error("the page of lab/dut1 shows another sink, or not its own")
	}
	all := e.Do("GET", "/projects/lab/logs", cookie, nil).Body.String()
	if !strings.Contains(all, "U-Boot 2024.04") || !strings.Contains(all, "Started cafard push") {
		t.Error("the logs of lab lack a sink")
	}
	// The filters stay on the page; Live has every one of them.
	if !strings.Contains(all, `<a class="btn text" href="/projects/lab/logs">Reset</a>`) || !strings.Contains(all, `href="/live?streams=%2A%3Alab%2F%2A"`) {
		t.Error("the logs of lab do not keep their filters at home, or lost the way to Live")
	}
	for _, path := range []string{"/projects/lab/logs/no*such", "/projects/nope/logs"} {
		if rec := e.Do("GET", path, cookie, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
}

// Nobody but the admin sees admin items, and the admin sees them on the
// admin interface only.
func TestOnlyTheAdminSeesAdminItems(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	e.Principal("bob", auth.KindHuman, auth.ScopeRead, auth.ScopeMessage)
	bob := e.Session("bob", auth.ScopeRead, auth.ScopeMessage)
	for _, path := range []string{"/", "/projects/lab/c/dut1", "/projects/lab/logs", "/dm"} {
		body := e.Do("GET", path, bob, nil).Body.String()
		for _, admin := range []string{`href="/admin"`, `href="/projects/lab/settings"`, `action="/projects"`, `href="/agents"`, `href="/pair"`} {
			if strings.Contains(body, admin) {
				t.Errorf("%s shows bob %s", path, admin)
			}
		}
	}
	for _, path := range []string{"/admin", "/projects/lab/settings", "/projects"} {
		if rec := e.Do("GET", path, bob, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s for bob: %d", path, rec.Code)
		}
		if rec := e.DoPublic("GET", path, e.Admin(), nil); rec.Code != http.StatusNotFound && rec.Code != http.StatusSeeOther {
			t.Errorf("%s on the public interface: %d", path, rec.Code)
		}
	}
	home := e.Do("GET", "/", e.Admin(), nil).Body.String()
	for _, want := range []string{`href="/admin"`, `action="/projects"`, "New project"} {
		if !strings.Contains(home, want) {
			t.Errorf("the admin's home lacks %q", want)
		}
	}
}
