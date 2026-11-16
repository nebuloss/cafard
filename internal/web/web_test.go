package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web"
	"github.com/nebuloss/cafard/internal/web/api"
	"github.com/nebuloss/cafard/internal/web/assets"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

var update = flag.Bool("update", false, "rewrite internal/web/testdata/pages/*.html")

// env is the web stack with the real messaging service (track D).
type env struct {
	*webtest.Env
	svc *messaging.Service
}

func newEnv(t *testing.T, o webtest.Options) *env {
	t.Helper()
	e := &env{Env: webtest.New(t, o)}
	e.svc = e.StartMessaging()
	if err := assets.Register(e.Mux); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RegisterMessages(e.Mux, mcpserver.New(e.Deps, mcpserver.Config{}), e.Deps, e.svc); err != nil {
		t.Fatal(err)
	}
	if _, err := web.Register(e.Mux, nil, e.Deps); err != nil {
		t.Fatal(err)
	}
	return e
}

// seed gives the pages something to show: two sources, lines, a marker,
// and the chats of seedChats.
func seed(e *env) {
	e.Status.Declare(status.Source{Stream: "serial:dut1", Transport: "rfc2217"})
	e.Commit(
		webtest.Line("serial:dut1", "U-Boot 2024.04 (Nov 16 2026)"),
		store.Entry{Stream: "serial:dut1", Kind: store.KindLine, Severity: 3, Text: "\x1b[31mdwc3 fe800000.usb: failed to initialize core: -110\x1b[0m"},
		store.Entry{Stream: "serial:dut1", Kind: store.KindMarker, Severity: store.DefaultSeverity, Text: "codex-build: flashed linux-6.18-rc3", Sender: "codex-build"},
		webtest.Line("syslog:bench-pi", "systemd[1]: Started cafard push."),
	)
	seedChats(e)
}

// The tpl-* twins each page lists, and the slots each twin carries
// (CONTRACTS.md 6.2). Track D adds its pages and twins here.
var twinSlots = map[string][]string{
	"tpl-line":        {"time", "stream", "sev", "text"},
	"tpl-marker":      {"time", "text"},
	"tpl-stream-dot":  {},
	"tpl-stream-card": {"name", "status", "transport", "rate", "last", "preview", "dropped", "claim", "line-state", "open"},
	"tpl-toast":       {"text", "action"},
	// Track D.
	"tpl-chat-row":           {"name", "preview", "time", "unread", "questions", "dots"},
	"tpl-message":            {"sender", "sender-kind", "time", "body", "reply-quote", "attachments", "seen-by", "menu"},
	"tpl-question":           {"sender", "time", "body", "choices", "reply-form", "answer"},
	"tpl-choice":             {"label"},
	"tpl-event-row":          {"stream", "time", "text", "open"},
	"tpl-image":              {"img", "more"},
	"tpl-log-attachment":     {"streams", "title", "lines", "span", "preview", "open", "state"},
	"tpl-attachment-chip":    {"label", "remove"},
	"tpl-upload-status":      {"label", "progress", "retry"},
	"tpl-needs-you-question": {"sender", "time", "body", "choices", "reply-form", "answer", "chat"},
}

var pageTwins = map[string][]string{
	"signin":  {"tpl-toast"},
	"streams": {"tpl-stream-dot", "tpl-stream-card", "tpl-toast"},
	"agents":  {"tpl-toast"},
	"pair":    {"tpl-toast"},
	"live":    {"tpl-line", "tpl-marker", "tpl-attachment-chip", "tpl-upload-status", "tpl-toast"},
	// Track D.
	"home": {"tpl-chat-row", "tpl-stream-dot", "tpl-message", "tpl-question", "tpl-choice", "tpl-event-row", "tpl-line",
		"tpl-image", "tpl-log-attachment", "tpl-attachment-chip", "tpl-upload-status", "tpl-needs-you-question", "tpl-toast"},
	"chat": {"tpl-chat-row", "tpl-stream-dot", "tpl-message", "tpl-question", "tpl-choice", "tpl-event-row", "tpl-line",
		"tpl-marker", "tpl-image", "tpl-log-attachment", "tpl-attachment-chip", "tpl-upload-status", "tpl-toast"},
	"drop": {"tpl-image", "tpl-attachment-chip", "tpl-upload-status", "tpl-toast"},
}

var pagePaths = map[string]string{
	"signin": "/signin", "streams": "/streams", "agents": "/agents", "pair": "/pair", "live": "/live",
	"home": "/", "chat": "/c/dut1", "drop": "/drop",
}

var templateRE = regexp.MustCompile(`(?s)<template id="(tpl-[a-z-]+)">(.*?)</template>`)

func twinsOf(body string) map[string]string {
	out := map[string]string{}
	for _, m := range templateRE.FindAllStringSubmatch(body, -1) {
		out[m[1]] = m[2]
	}
	return out
}

var scriptTag = regexp.MustCompile(`<script type="module" src="/app/main.js"></script>`)

// TestEveryPageCarriesItsTwins is the template test of CONTRACTS.md 6.4.
// It also writes the pages for the TypeScript renderer tests.
func TestEveryPageCarriesItsTwins(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	names := make([]string, 0, len(pageTwins))
	for page := range pageTwins {
		names = append(names, page)
	}
	sort.Strings(names)
	for _, page := range names {
		c := cookie
		if page == "signin" {
			c = nil
		}
		rec := e.Do("GET", pagePaths[page], c, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d\n%s", page, rec.Code, rec.Body)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `data-page="`+page+`"`) {
			t.Errorf("%s: no data-page", page)
		}
		twins := twinsOf(body)
		for _, id := range pageTwins[page] {
			content, ok := twins[id]
			if !ok {
				t.Errorf("%s: no <template id=%q>", page, id)
				continue
			}
			for _, slot := range twinSlots[id] {
				if !strings.Contains(content, `data-slot="`+slot+`"`) {
					t.Errorf("%s: %s has no slot %q", page, id, slot)
				}
			}
		}
		// The bundle's tag depends on whether main.js was built; the
		// fixture must not.
		fixture := scriptTag.ReplaceAllString(body, "")
		path := filepath.Join("testdata", "pages", page+".html")
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		disk, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(disk, []byte(fixture)) {
			t.Errorf("%s is stale (run go test ./internal/web -update)", path)
		}
	}
}

// noBundle lists, per page, what must be in the server-rendered HTML for
// the page to work with no script at all. Track D adds the chat pages.
var noBundle = map[string][]string{
	"/streams": {
		`data-stream="serial:dut1"`, "rfc2217", "flashed linux-6.18-rc3", `href="/live?streams=serial%3Adut1"`,
		`data-stream="syslog:bench-pi"`, "systemd[1]: Started cafard push.",
	},
	"/live": {
		"U-Boot 2024.04", `class="marker"`, "flashed linux-6.18-rc3", `<form class="filter-bar" method="get" action="/live"`,
		"Live updates need JavaScript",
	},
	// Track D: the chats (noBundleChats in chat_test.go).
}

func TestThePageWorksWithoutTheBundle(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	seed(e)
	cookie := e.Admin()
	for path, wants := range noBundleChats(e) {
		noBundle[path] = wants
	}
	for path, wants := range noBundle {
		body := e.Do("GET", path, cookie, nil).Body.String()
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s: %q is not server-rendered", path, want)
			}
		}
		// Navigation, theme and sign out are links and plain forms.
		for _, want := range []string{`href="/live"`, `href="/streams"`, `href="/agents"`, `href="/pair"`,
			`action="/theme"`, `action="/signout"`, `aria-current="page"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: shell lost %q", path, want)
			}
		}
	}
}

func TestPagesEscapeHostileLogLines(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	cookie := e.Admin()
	e.Commit(
		webtest.Line("push:evil", "<script>alert(1)</script>"),
		webtest.Line("push:evil", "\x1b[31m<img src=x onerror=alert(2)>\x1b[0m\x1b]0;owned\x07"),
	)
	for _, path := range []string{"/live", "/streams"} {
		body := e.Do("GET", path, cookie, nil).Body.String()
		for _, bad := range []string{"<script>alert", "<img src=x", "\x1b", "owned"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: %q reached the page", path, bad)
			}
		}
		if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") && path == "/live" {
			t.Errorf("%s: the line is not shown escaped", path)
		}
	}
	if body := e.Do("GET", "/live", cookie, nil).Body.String(); !strings.Contains(body, `<span class="ansi-fg-1">&lt;img`) {
		t.Error("ANSI red is not rendered as a class")
	}
}

func TestSignInWithATokenOpensASession(t *testing.T) {
	e := newEnv(t, webtest.Options{Secure: true})
	admin := e.Principal("admin", auth.KindHuman, auth.ScopeRead, auth.ScopeAdmin)
	agent := e.Principal("codex", auth.KindAgent, auth.ScopeRead)

	if rec := e.Do("GET", "/streams", nil, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/signin?next=%2Fstreams" {
		t.Errorf("no session: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := e.Do("GET", "/", nil, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/signin" {
		t.Errorf("home without a session: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	rec := e.Do("POST", "/signin", nil, url.Values{"token": {agent}})
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), agent) {
		t.Errorf("agent token: %d", rec.Code)
	}
	rec = e.Do("POST", "/signin", nil, url.Values{"token": {"cfd_wrong"}})
	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "cfd_wrong") {
		t.Errorf("wrong token: %d, or echoed", rec.Code)
	}

	rec = e.Do("POST", "/signin", nil, url.Values{"token": {admin}, "next": {"/streams"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/streams" {
		t.Fatalf("sign in: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	c := sessionCookie(t, rec)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("cookie attributes: %+v", c)
	}
	if strings.Contains(c.Value, admin) || !strings.HasPrefix(c.Value, auth.SessionPrefix) {
		t.Error("the cookie carries the token")
	}
	if rec := e.Do("GET", "/streams", c, nil); rec.Code != http.StatusOK {
		t.Errorf("signed in: %d", rec.Code)
	}
	// A session copies the token's scopes: no message scope here.
	if rec := e.Do("POST", "/api/marks", c, `{"stream":"x:y","label":"z"}`); rec.Code != http.StatusForbidden {
		t.Errorf("scope not copied: %d", rec.Code)
	}
	if rec := e.Do("GET", "/signin?next=/live", c, nil); rec.Header().Get("Location") != "/live" {
		t.Errorf("signed-in /signin: %s", rec.Header().Get("Location"))
	}
	// Off-site next targets are ignored.
	rec = e.Do("POST", "/signin", nil, url.Values{"token": {admin}, "next": {"//evil.example"}})
	if rec.Header().Get("Location") != "/" {
		t.Errorf("off-site next: %s", rec.Header().Get("Location"))
	}

	rec = e.Do("POST", "/signout", c, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/signin" {
		t.Errorf("sign out: %d", rec.Code)
	}
	if rec := e.Do("GET", "/streams", c, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("after sign out: %d", rec.Code)
	}
}

func sessionCookie(t *testing.T, rec interface{ Result() *http.Response }) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie && c.Value != "" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestATokenIsShownOnlyOnce(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	cookie := e.Admin()
	rec := e.Do("POST", "/tokens", cookie, url.Values{"principal": {"claude-laptop"}, "kind": {"agent"}, "label": {"laptop"}, "scopes": {"read", "message"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d\n%s", rec.Code, rec.Body)
	}
	m := regexp.MustCompile(`cfd_[A-Za-z0-9_-]{43}`).FindString(rec.Body.String())
	if m == "" {
		t.Fatal("the new token is not shown")
	}
	if !strings.Contains(rec.Body.String(), "claude mcp add --transport http cafard https://cafard.lan/mcp") {
		t.Error("no Claude Code snippet")
	}
	for _, path := range []string{"/agents", "/api/tokens", "/pair"} {
		if body := e.Do("GET", path, cookie, nil).Body.String(); strings.Contains(body, m) {
			t.Errorf("%s shows the token again", path)
		}
	}
	// It works, as a bearer token of the new agent.
	rec = e.Do("GET", "/api/me", nil, nil, "Authorization", "Bearer "+m)
	var me dto.Me
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.Name != "claude-laptop" || me.Kind != dto.KindAgent {
		t.Errorf("me = %+v %v", me, err)
	}
	// And the JSON API shows its own once too.
	rec = e.Do("POST", "/api/tokens", cookie, `{"principal":"bench-pi","kind":"device","label":"pi","scopes":["ingest"]}`, "Sec-Fetch-Site", "same-origin")
	var created dto.TokenCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || rec.Code != http.StatusCreated || !strings.HasPrefix(created.Secret, "cfd_") {
		t.Fatalf("api create: %d %s", rec.Code, rec.Body)
	}
	if body := e.Do("GET", "/api/tokens", cookie, nil).Body.String(); strings.Contains(body, created.Secret) {
		t.Error("/api/tokens lists a secret")
	}
}

// Every write a session can make. A cookie is all a forged cross-site
// form would carry, so this check is the whole CSRF defence.
var sessionWrites = []struct{ method, path string }{
	{"POST", "/signin"}, {"POST", "/signout"}, {"POST", "/theme"}, {"POST", "/pair"},
	{"POST", "/tokens"}, {"POST", "/tokens/1/revoke"}, {"POST", "/sessions/1/revoke"},
	{"POST", "/api/marks"}, {"POST", "/api/tokens"}, {"DELETE", "/api/tokens/1"},
	{"DELETE", "/api/sessions/1"}, {"POST", "/api/pairings"},
}

func TestCrossOriginWritesAreRefused(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	cookie := e.Admin()
	for _, w := range sessionWrites {
		for _, h := range [][]string{{"Sec-Fetch-Site", "cross-site"}, {"Sec-Fetch-Site", "same-site"}, {"Origin", "https://evil.example"}} {
			rec := e.Do(w.method, w.path, cookie, url.Values{"theme": {"dark"}}, h...)
			var body dto.Error
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != http.StatusForbidden || body.Error.Code != dto.CodeCrossOrigin {
				t.Errorf("%s %s with %s: %d %s", w.method, w.path, h[0]+"="+h[1], rec.Code, body.Error.Code)
			}
		}
	}
	// The same post from the page itself goes through.
	if rec := e.Do("POST", "/theme", cookie, url.Values{"theme": {"dark"}}, "Sec-Fetch-Site", "same-origin"); rec.Code != http.StatusSeeOther {
		t.Errorf("same-origin theme: %d", rec.Code)
	}
}

func TestThemeIsRenderedOnTheFirstPaint(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	cookie := e.Admin()
	rec := e.Do("POST", "/theme", cookie, url.Values{"theme": {"dark"}, "next": {"/live"}})
	if rec.Header().Get("Location") != "/live" {
		t.Errorf("theme redirect %q", rec.Header().Get("Location"))
	}
	var theme *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "cafard_theme" {
			theme = c
		}
	}
	if theme == nil || theme.Value != "dark" {
		t.Fatalf("theme cookie %+v", theme)
	}
	req := e.Do("GET", "/live", cookie, nil, "Cookie", cookie.Name+"="+cookie.Value+"; cafard_theme=dark")
	if !strings.Contains(req.Body.String(), `<html lang="en" data-theme="dark">`) {
		t.Error("dark theme not on <html>")
	}
	req = e.Do("GET", "/live", cookie, nil, "Cookie", cookie.Name+"="+cookie.Value+"; cafard_theme=purple")
	if strings.Contains(req.Body.String(), "data-theme=") {
		t.Error("an unknown theme is applied")
	}
}

func TestPairingSignsAPhoneInOnce(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	cookie := e.Admin()
	if rec := e.Do("POST", "/pair", cookie, url.Values{"new_name": {"guillaume-phone"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("pair: %d\n%s", rec.Code, rec.Body)
	}
	page := e.Do("GET", "/pair", cookie, nil).Body.String()
	link := regexp.MustCompile(`https://cafard\.lan/p/([a-z0-9]+)`).FindStringSubmatch(page)
	if link == nil {
		t.Fatalf("no pairing link on\n%s", page)
	}
	qr := regexp.MustCompile(`src="(/pair/\d+/qr\.svg)"`).FindStringSubmatch(page)
	if qr == nil {
		t.Fatal("no QR image")
	}
	if rec := e.Do("GET", qr[1], cookie, nil); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(rec.Body.String(), "<svg") {
		t.Errorf("qr: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := e.Do("GET", qr[1], nil, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("qr without a session: %d", rec.Code)
	}

	rec := e.Do("GET", "/p/"+link[1], nil, nil, "User-Agent", "Mozilla/5.0 (Linux; Android 15) Chrome/141.0")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("redeem: %d", rec.Code)
	}
	phone := sessionCookie(t, rec)
	var me dto.Me
	_ = json.Unmarshal(e.Do("GET", "/api/me", phone, nil).Body.Bytes(), &me)
	if me.Name != "guillaume-phone" || me.Kind != dto.KindHuman || me.SessionID == 0 || len(me.Scopes) != 3 {
		t.Errorf("phone me = %+v", me)
	}
	if rec := e.Do("GET", "/p/"+link[1], nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("second redeem: %d", rec.Code)
	}
	if strings.Contains(e.Do("GET", "/pair", cookie, nil).Body.String(), link[1]) {
		t.Error("a used code is still shown")
	}
	// The phone's session is listed and can be revoked by the admin.
	if !strings.Contains(e.Do("GET", "/pair", cookie, nil).Body.String(), "Chrome on Android (paired)") {
		t.Error("the phone session is not listed")
	}
	if rec := e.Do("POST", "/sessions/"+itoa(me.SessionID)+"/revoke", cookie, url.Values{}); rec.Code != http.StatusSeeOther {
		t.Errorf("revoke: %d", rec.Code)
	}
	if rec := e.Do("GET", "/api/me", phone, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked phone: %d", rec.Code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestAPersonCanRevokeOnlyTheirOwnSessions(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	admin := e.Admin()
	e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	alice := e.Session("alice", auth.ScopeRead)
	var list struct{ Sessions []dto.Session }
	_ = json.Unmarshal(e.Do("GET", "/api/sessions", alice, nil).Body.Bytes(), &list)
	if len(list.Sessions) != 1 || list.Sessions[0].Principal != "alice" || !list.Sessions[0].Current {
		t.Errorf("alice sees %+v", list.Sessions)
	}
	_ = json.Unmarshal(e.Do("GET", "/api/sessions", admin, nil).Body.Bytes(), &list)
	if len(list.Sessions) != 2 {
		t.Errorf("admin sees %d sessions", len(list.Sessions))
	}
	var adminID int64
	for _, s := range list.Sessions {
		if s.Principal == "admin" {
			adminID = s.ID
		}
	}
	if rec := e.Do("DELETE", "/api/sessions/"+itoa(adminID), alice, nil); rec.Code != http.StatusNotFound {
		t.Errorf("alice revoking admin: %d", rec.Code)
	}
	if rec := e.Do("POST", "/sessions/"+itoa(adminID)+"/revoke", alice, url.Values{}); rec.Code != http.StatusNotFound {
		t.Errorf("alice revoking admin by form: %d", rec.Code)
	}
}

func TestTheExposureGuardCountsAdminSessions(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	ctx := context.Background()
	guard := func() error {
		return auth.ExposureGuard(ctx, "0.0.0.0:8790", e.Tokens.CountAdminTokens, e.Sessions.CountAdmin)
	}
	e.Principal("admin", auth.KindHuman, auth.ScopeAdmin)
	toks, _ := e.Tokens.ListTokens(ctx, "")
	_ = e.Tokens.RevokeToken(ctx, toks[0].ID)
	if guard() == nil {
		t.Fatal("guard passed with no admin credential")
	}
	e.Session("admin", auth.ScopeRead, auth.ScopeAdmin)
	if err := guard(); err != nil {
		t.Errorf("an admin session does not count: %v", err)
	}
}

// Every icon a template names is in the sprite, and the root files are served.
func TestIconsAndRootFilesAreServed(t *testing.T) {
	e := newEnv(t, webtest.Options{})
	sprite := e.Do("GET", "/icons.svg", nil, nil)
	if sprite.Code != 200 || !strings.HasPrefix(sprite.Header().Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("/icons.svg: %d %s", sprite.Code, sprite.Header().Get("Content-Type"))
	}
	if rec := e.Do("GET", "/static/icons.svg", nil, nil); rec.Code != 200 {
		t.Errorf("/static/icons.svg: %d", rec.Code)
	}
	if rec := e.Do("GET", "/manifest.webmanifest", nil, nil); rec.Code != 200 || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Errorf("manifest: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	cookie := e.Admin()
	used := map[string]bool{}
	for _, path := range []string{"/streams", "/live", "/agents", "/pair", "/"} {
		for _, m := range regexp.MustCompile(`icons\.svg#([a-z_]+)`).FindAllStringSubmatch(e.Do("GET", path, cookie, nil).Body.String(), -1) {
			used[m[1]] = true
		}
	}
	for id := range used {
		if !strings.Contains(sprite.Body.String(), `<symbol id="`+id+`"`) {
			t.Errorf("icon %q is not in the sprite", id)
		}
	}
}
