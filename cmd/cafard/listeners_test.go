package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/config"
	"github.com/nebuloss/cafard/internal/dto"
)

// The admin routes, including the one that needs no session at all: the
// password form's POST is what a guesser would aim at.
var adminRoutes = []struct{ method, path string }{
	{"POST", "/signin"}, {"POST", "/password"},
	{"GET", "/agents"}, {"POST", "/tokens"}, {"POST", "/tokens/1/revoke"},
	{"GET", "/pair"}, {"POST", "/pair"}, {"GET", "/pair/1/qr.svg"},
	{"GET", "/api/tokens"}, {"POST", "/api/tokens"}, {"DELETE", "/api/tokens/1"}, {"POST", "/api/pairings"},
	{"GET", "/projects"}, {"POST", "/projects"}, {"GET", "/projects/lab"}, {"POST", "/projects/lab/retention"},
	{"POST", "/projects/lab/channels"}, {"POST", "/projects/lab/channels/dut1/kind"}, {"POST", "/projects/lab/sinks"}, {"POST", "/projects/lab/sinks/dut1"},
	{"POST", "/projects/lab/sinks/dut1/delete"}, {"POST", "/projects/lab/sinks/dut1/tokens"},
	{"POST", "/projects/lab/grants"}, {"POST", "/projects/lab/grants/1/delete"}, {"POST", "/projects/lab/pairings"},
}

// What a phone, an agent or a device uses must answer on both.
var publicRoutes = []struct{ method, path string }{
	{"GET", "/healthz"}, {"GET", "/signin"}, {"GET", "/"}, {"GET", "/live"}, {"GET", "/streams"},
	{"GET", "/api/me"}, {"POST", "/api/log"}, {"POST", "/mcp"}, {"GET", "/p/nope"}, {"GET", "/static/icons.svg"},
}

func buildApp(t *testing.T) *app {
	t.Helper()
	cfg := testConfig()
	cfg.DataDir = t.TempDir()
	st, err := openStore(context.Background(), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, err := build(&cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func statusOf(h http.Handler, method, path string, cookies ...*http.Cookie) int {
	r := httptest.NewRequest(method, path, strings.NewReader(""))
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

// The point of admin_listen: publishing http_listen must not publish the
// admin routes with it. The separation is cafard's own, so it holds
// however the proxy in front is configured, and with an admin cookie too.
func TestSplitListenersDoNotServeEachOther(t *testing.T) {
	a := buildApp(t)
	secret, err := a.deps.Admin.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: auth.AdminCookie, Value: secret}
	for _, r := range adminRoutes {
		if got := statusOf(a.public, r.method, r.path); got != http.StatusNotFound {
			t.Errorf("%s %s on the public listener = %d, want 404", r.method, r.path, got)
		}
		if got := statusOf(a.public, r.method, r.path, cookie); got != http.StatusNotFound {
			t.Errorf("%s %s on the public listener with the admin cookie = %d, want 404", r.method, r.path, got)
		}
		if got := statusOf(a.admin, r.method, r.path); got == http.StatusNotFound {
			t.Errorf("%s %s on the admin listener = 404", r.method, r.path)
		}
	}
	for _, r := range publicRoutes {
		for name, h := range map[string]http.Handler{"public": a.public, "admin": a.admin} {
			if got := statusOf(h, r.method, r.path); got == http.StatusNotFound && r.path != "/p/nope" {
				t.Errorf("%s %s on the %s listener = 404", r.method, r.path, name)
			}
		}
	}
	// The admin cookie opens the admin's pages on the admin listener only.
	if got := statusOf(a.admin, "GET", "/agents", cookie); got != http.StatusOK {
		t.Errorf("GET /agents signed in on the admin listener = %d", got)
	}
	if got := statusOf(a.public, "GET", "/api/me", cookie); got != http.StatusUnauthorized {
		t.Errorf("GET /api/me with the admin cookie on the public listener = %d", got)
	}
}

// Unset admin_listen keeps one listener serving everything, which is what
// every existing install has.
func TestOneListenerServesEverythingWithoutAdminListen(t *testing.T) {
	a := buildApp(t)
	cfg := testConfig()
	ls := a.listeners(&cfg)
	if len(ls) != 1 || ls[0].address != cfg.HTTPListen {
		t.Fatalf("listeners = %+v", ls)
	}
	for _, r := range append(adminRoutes, publicRoutes[:6]...) {
		if got := statusOf(ls[0].handler, r.method, r.path); got == http.StatusNotFound {
			t.Errorf("%s %s on the only listener = 404", r.method, r.path)
		}
	}
	cfg.AdminListen = "127.0.0.1:8791"
	ls = a.listeners(&cfg)
	if len(ls) != 2 || ls[0].address != cfg.HTTPListen || ls[1].address != cfg.AdminListen ||
		statusOf(ls[0].handler, "GET", "/agents") != http.StatusNotFound || statusOf(ls[1].handler, "GET", "/agents") == http.StatusNotFound {
		t.Errorf("split listeners = %+v", ls)
	}
}

// Through real sockets: the admin signs in and makes a sign-in link on the
// admin interface, a phone spends it on the public one.
func TestSplitInterfacesEndToEnd(t *testing.T) {
	s := startServer(t, func(c *config.Config) { c.AdminListen = "127.0.0.1:0" })
	if s.adminBase == s.base {
		t.Fatal("one address for both interfaces")
	}
	resp, err := noRedirect.PostForm(s.base+"/signin", url.Values{"password": {s.password}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("password sign-in on the public interface: %d", resp.StatusCode)
	}
	admin := s.session()
	resp, body := s.browserAt(s.adminBase, admin, "POST", "/api/pairings", "application/json", strings.NewReader(`{"new_name":"guillaume-phone"}`))
	var p dto.Pairing
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &p) != nil || !strings.Contains(p.URL, "/p/") {
		t.Fatalf("pairing: %d %s", resp.StatusCode, body)
	}
	code := p.URL[strings.LastIndex(p.URL, "/p/")+3:]
	resp, err = noRedirect.Get(s.base + "/p/" + code)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var phone *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie {
			phone = c
		}
	}
	if resp.StatusCode != http.StatusSeeOther || phone == nil {
		t.Fatalf("redeem on the public interface: %d", resp.StatusCode)
	}
	if resp, _ := s.browserAt(s.base, phone, "GET", "/api/me", "", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("the phone on the public interface: %d", resp.StatusCode)
	}
	if resp, _ := s.browserAt(s.base, admin, "GET", "/api/me", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the admin cookie on the public interface: %d", resp.StatusCode)
	}
	if resp, _ := s.browserAt(s.adminBase, admin, "GET", "/agents", "", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("the admin on the admin interface: %d", resp.StatusCode)
	}
}
