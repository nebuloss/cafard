package join

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"cafard/internal/service/access"
	"cafard/internal/store"
	"cafard/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Offline(m) }

var ctx = context.Background()

type rig struct {
	srv     *httptest.Server
	acc     *access.Service
	admin   access.Who
	camille store.Identity
	devCode store.Identity
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cafard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	acc := access.New(st)
	r := &rig{acc: acc, admin: access.Admin("test")}
	r.camille, _ = acc.CreateIdentity(ctx, r.admin, access.NewIdentity{Kind: store.Person, Name: "camille", DisplayName: "Camille"})
	r.devCode, _ = acc.CreateIdentity(ctx, r.admin, access.NewIdentity{Kind: store.Agent, Name: "dev-code"})
	mux := http.NewServeMux()
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	New(nil, acc, "https://cafard.example").Register(mux)
	return r
}

func (r *rig) link(t *testing.T, who store.Identity) string {
	t.Helper()
	code, _, err := r.acc.IssueLink(ctx, r.admin, who, 0)
	if err != nil {
		t.Fatal(err)
	}
	return r.srv.URL + "/join/" + code
}

func get(t *testing.T, u, ua string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("GET", u, nil)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// A person's link is a page with a Sign in button. Opening it spends
// nothing, as a messenger's preview fetches it before the person taps it;
// the button signs this browser in and opens the chat.
func TestPersonSignsInWithTheirLink(t *testing.T) {
	r := newRig(t)
	u := r.link(t, r.camille)
	for i := 0; i < 2; i++ {
		code, body, h := get(t, u, "Mozilla/5.0 (iPhone)")
		if code != 200 || !strings.Contains(body, "Sign in as Camille") || !strings.Contains(body, `<form method="post">`) {
			t.Fatalf("page, opening %d: %d\n%s", i+1, code, body)
		}
		if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") || h.Get("Cache-Control") != "no-store" {
			t.Errorf("headers: %v", h)
		}
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.PostForm(u, url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == "cafard_chat" {
			cookie = c.Value
		}
	}
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" || cookie == "" {
		t.Fatalf("sign in: %d %q, cookie %q", resp.StatusCode, resp.Header.Get("Location"), cookie)
	}
	if who, _, err := r.acc.Session(ctx, access.ChatSurface, cookie, "x"); err != nil || who.Name != "camille" {
		t.Errorf("the sign-in: %+v %v", who, err)
	}
	if code, body, _ := get(t, u, ""); code != http.StatusGone || !strings.Contains(body, "no longer works") {
		t.Errorf("after use: %d %s", code, body)
	}
}

func TestAgentLinkIsAScript(t *testing.T) {
	r := newRig(t)
	u := r.link(t, r.devCode)
	code, body, h := get(t, u, "curl/8.5.0")
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/x-shellscript") {
		t.Fatalf("script: %d %v", code, h)
	}
	for _, want := range []string{"#!/bin/sh", "url='https://cafard.example'", "/token", "the agent dev-code", "umask 077",
		"claude mcp add --scope user --transport http cafard \"$url/mcp\"", "claude mcp remove --scope user cafard"} {
		if !strings.Contains(body, want) {
			t.Errorf("script lacks %q:\n%s", want, body)
		}
	}
	// The script asks; the answer is the token, once.
	resp, _ := http.Post(u+"/token", "", nil)
	tok, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(tok), "cfd_ag_") {
		t.Fatalf("token: %d %s", resp.StatusCode, tok)
	}
	resp, _ = http.Post(u+"/token", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Errorf("a second exchange: %d", resp.StatusCode)
	}
}

// A spent link, run with curl -fsSL, still prints why: status 200 and a
// script that fails, because -f would swallow an error status.
func TestSpentLinkInAPipeSaysWhy(t *testing.T) {
	r := newRig(t)
	code, body, _ := get(t, r.srv.URL+"/join/nothing-like-a-code", "curl/8.5.0")
	if code != 200 || !strings.Contains(body, "exit 1") || !strings.Contains(body, "no longer works") {
		t.Errorf("spent, via curl: %d %s", code, body)
	}
	u := r.link(t, r.camille)
	if code, body, _ := get(t, u, "curl/8.5.0"); !strings.Contains(body, "open it in a browser") || !strings.Contains(body, "exit 1") {
		t.Errorf("a person's link via curl: %d %s", code, body)
	}
}

func TestCrossSitePostIsRefused(t *testing.T) {
	r := newRig(t)
	u := r.link(t, r.camille)
	req, _ := http.NewRequest("POST", u, strings.NewReader("password=aaaaaaaaaaaa&again=aaaaaaaaaaaa"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("cross-site: %d", resp.StatusCode)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("shellQuote = %s", got)
	}
}
