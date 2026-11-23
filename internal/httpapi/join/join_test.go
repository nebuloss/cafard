package join

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"cafard/internal/httpapi/httpx"
	"cafard/internal/install"
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
	New(nil, acc, httpx.NewPublicURL("https://cafard.example")).Register(mux)
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

var tokenRE = regexp.MustCompile(`cfd_ag_[A-Z2-7a-z0-9]+`)

// An agent's link opened in a browser — or a messenger's preview — is a page
// with both commands, and spends nothing.
func TestAgentLinkInABrowserShowsTheCommands(t *testing.T) {
	r := newRig(t)
	u := r.link(t, r.devCode)
	pub := "https://cafard.example/join/" + u[strings.LastIndex(u, "/")+1:]
	for i := 0; i < 2; i++ {
		code, body, h := get(t, u, "Mozilla/5.0 (iPhone)")
		if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/html") {
			t.Fatalf("page: %d %v", code, h)
		}
		for _, want := range []string{"Set up dev-code", "Linux / macOS", "Windows (PowerShell)",
			"curl -fsSL &#39;" + pub + "?shell=sh&amp;client=claude-code&#39; | sh", "irm &#39;" + pub + "?shell=ps1&amp;client=claude-code&#39; | iex",
			`<a href="?client=claude-code" aria-current="true">Claude Code</a>`, `<a href="?client=opencode">opencode</a>`} {
			if !strings.Contains(body, want) {
				t.Errorf("page lacks %q:\n%s", want, body)
			}
		}
		if tokenRE.MatchString(body) {
			t.Error("the page carries a token")
		}
	}
	// Another client is a link to the same page.
	if _, body, _ := get(t, u+"?client=crush", "Mozilla/5.0 (iPhone)"); !strings.Contains(body, "?shell=sh&amp;client=crush&#39;") ||
		!strings.Contains(body, `<a href="?client=crush" aria-current="true">crush</a>`) {
		t.Errorf("crush chosen:\n%s", body)
	}
	if code, body, _ := get(t, u+"?shell=sh", "curl/8.5.0"); code != 200 || !tokenRE.MatchString(body) {
		t.Errorf("after the page, the link no longer worked: %d\n%s", code, body)
	}
}

// From a shell the link is the script, carrying the token, once: sh for curl
// with or without shell=, PowerShell for irm.
func TestAgentLinkInAShellIsTheScript(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct{ query, ua, kind, first string }{
		{"?shell=sh", "curl/8.5.0", "text/x-shellscript", "#!/bin/sh"},
		{"", "curl/8.5.0", "text/x-shellscript", "#!/bin/sh"},
		{"?shell=sh", "Wget/1.21", "text/x-shellscript", "#!/bin/sh"},
		{"?shell=ps1", "Mozilla/5.0 (Windows NT 10.0; Microsoft Windows 10.0.22631; en-US) PowerShell/7.4.6", "text/plain", "# cafard"},
		{"?shell=ps1", "Mozilla/5.0 (Windows NT; Windows NT 10.0; en-US) WindowsPowerShell/5.1.22621.4391", "text/plain", "# cafard"},
	} {
		u := r.link(t, r.devCode)
		code, body, h := get(t, u+c.query, c.ua)
		if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), c.kind) || !strings.HasPrefix(body, c.first) {
			t.Fatalf("%s %s: %d %v\n%s", c.ua, c.query, code, h, body)
		}
		tok := tokenRE.FindString(body)
		if who, err := r.acc.Authenticate(ctx, tok, "x"); err != nil || who.Name != "dev-code" {
			t.Errorf("%s: the script's token: %v", c.ua, err)
		}
		for _, want := range []string{"the agent 'dev-code'", ".config/cafard/token",
			"'mcp' 'add' '--scope' 'user' '--transport' 'http' 'cafard' 'https://cafard.example/mcp' '--header' 'Authorization: Bearer " + tok + "'"} {
			if strings.HasPrefix(c.first, "#!") && !strings.Contains(body, want) {
				t.Errorf("%s: script lacks %q:\n%s", c.ua, want, body)
			}
		}
		if code, body, _ := get(t, u+c.query, c.ua); code != 200 || tokenRE.MatchString(body) || !strings.Contains(body, "no longer works") {
			t.Errorf("%s: a second fetch: %d\n%s", c.ua, code, body)
		}
	}
	// A shell cafard does not write for spends nothing.
	u := r.link(t, r.devCode)
	if _, body, _ := get(t, u+"?shell=fish", "curl/8.5.0"); !strings.Contains(body, "exit 1") || tokenRE.MatchString(body) {
		t.Errorf("shell=fish: %s", body)
	}
	if _, body, _ := get(t, u+"?shell=sh", "curl/8.5.0"); !tokenRE.MatchString(body) {
		t.Errorf("after shell=fish the link no longer worked:\n%s", body)
	}
}

// The script, run for real against a scratch home, writes the token for its
// user alone and registers cafard in Claude Code with it.
func TestAgentScriptRuns(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	r := newRig(t)
	_, body, _ := get(t, r.link(t, r.devCode)+"?shell=sh", "curl/8.5.0")
	tok := tokenRE.FindString(body)
	home, bin := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "claude.log")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh)
	cmd.Stdin = strings.NewReader(body)
	cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".config/cafard/token")); string(got) != tok+"\n" {
		t.Errorf("token file: %q", got)
	}
	if fi, err := os.Stat(filepath.Join(home, ".config/cafard/token")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("token file mode: %v", err)
	}
	calls, _ := os.ReadFile(log)
	want := "mcp remove --scope user cafard\nmcp add --scope user --transport http cafard https://cafard.example/mcp --header Authorization: Bearer " + tok + "\n"
	if string(calls) != want {
		t.Errorf("claude was run as\n%s\nwant\n%s", calls, want)
	}
	if !strings.Contains(string(out), "Claude Code: set up") {
		t.Errorf("output:\n%s", out)
	}
}

// What each client is given: cafard's entry alone, in the shape its docs
// and claudication's measurements say.
func TestAgentBundle(t *testing.T) {
	var b install.Bundle
	for _, c := range Clients {
		one := agentBundle("https://c", "dev-code", "cfd_ag_x", c)
		if err := one.Validate(); err != nil {
			t.Fatal(err)
		}
		b.Files = append(b.Files, one.Files...)
	}
	want := map[string]string{
		".codex/config.toml":              "[mcp_servers.cafard]\nurl = \"https://c/mcp\"\nbearer_token_env_var = \"CAFARD_TOKEN\"\n",
		".config/cafard/env.sh":           "export CAFARD_TOKEN='cfd_ag_x'\n",
		".config/crush/crush.json":        `{"mcp":{"cafard":{"headers":{"Authorization":"Bearer cfd_ag_x"},"type":"http","url":"https://c/mcp"}}}`,
		".config/opencode/opencode.jsonc": `{"mcp":{"cafard":{"headers":{"Authorization":"Bearer cfd_ag_x"},"oauth":false,"type":"remote","url":"https://c/mcp"}}}`,
		".config/cafard/token":            "cfd_ag_x\n",
	}
	for _, f := range b.Files {
		w, ok := want[f.Path]
		if !ok {
			continue
		}
		got := f.Body
		if strings.HasSuffix(f.Path, ".json") || strings.HasSuffix(f.Path, ".jsonc") {
			var v any
			if err := json.Unmarshal([]byte(got), &v); err != nil {
				t.Fatal(err)
			}
			c, _ := json.Marshal(v)
			got = string(c)
		}
		if got != w {
			t.Errorf("%s:\n%s\nwant\n%s", f.Path, got, w)
		}
		delete(want, f.Path)
	}
	if len(want) != 0 {
		t.Errorf("not written: %v", want)
	}
}

// The exchange older scripts make still works, once.
func TestTokenExchange(t *testing.T) {
	r := newRig(t)
	u := r.link(t, r.devCode)
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
	if code, body, _ := get(t, u+"?shell=ps1", "Mozilla/5.0 PowerShell/7.4.6"); code != 200 || !strings.HasPrefix(body, "Write-Error 'cafard: ") {
		t.Errorf("a person's link via irm: %d %s", code, body)
	}
	if code, body, _ := get(t, r.srv.URL+"/join/nothing-like-a-code?shell=ps1", "Mozilla/5.0 WindowsPowerShell/5.1"); code != 200 || !strings.Contains(body, "Write-Error") {
		t.Errorf("spent, via irm: %d %s", code, body)
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
