package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"cafard/internal/config"
	"cafard/internal/service/access"
)

type box struct {
	t    *testing.T
	cfg  config.Config
	path string
}

// newBox is an installed cafard: a config file and a state directory, as the
// service leaves them after its first start.
func newBox(t *testing.T) *box {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	// The file is what `cafard admin` reads; the ports in it are never bound.
	// The server in serve() runs from cfg, on ports the kernel picks.
	os.WriteFile(path, []byte("listen: 127.0.0.1:18330\nadmin-listen: 127.0.0.1:18331\nstate-dir: "+state+"\npublic-url: https://cafard.example\n"), 0o600)
	cfg := config.Default()
	cfg.Listen, cfg.AdminListen, cfg.StateDir, cfg.PublicURL = "127.0.0.1:0", "127.0.0.1:0", state, "https://cafard.example"
	return &box{t: t, cfg: cfg, path: path}
}

// admin runs `cafard admin -config … args…` and returns what it printed.
func (b *box) admin(args ...string) (string, int) {
	b.t.Helper()
	var out, errOut bytes.Buffer
	code := run(append([]string{"admin", "-config", b.path}, args...), &out, &errOut)
	return out.String() + errOut.String(), code
}

func (b *box) must(args ...string) string {
	b.t.Helper()
	out, code := b.admin(args...)
	if code != 0 {
		b.t.Fatalf("cafard admin %s: %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

var (
	tokenRE = regexp.MustCompile(`cfd_(ag|dv)_[a-z2-7]{52}`)
	linkRE  = regexp.MustCompile(`https://cafard\.example/join/([a-z2-7]{52})`)
)

func TestAdminFromAnEmptyInstall(t *testing.T) {
	b := newBox(t)

	// The box acts as the admin from the start: people are members only.
	if out := b.must("create", "person", "guillaume", "-name", "Guillaume"); !linkRE.MatchString(out) || !strings.Contains(out, "█") {
		t.Errorf("a person, with a join link and its QR code: %s", out)
	}
	b.must("create", "person", "camille")
	b.must("create", "project", "be98", "-quota", "2G", "-member", "guillaume")
	b.must("create", "project", "zeeos")

	dev := b.must("create", "device", "dev-build", "-description", "build output", "-project", "be98")
	devToken := tokenRE.FindString(dev)
	if !strings.HasPrefix(devToken, "cfd_dv_") || !strings.Contains(dev, "https://cafard.example/api/v1/send/be98/") {
		t.Fatalf("create device printed:\n%s", dev)
	}
	// An agent comes with an install link; its token is never printed.
	agent := b.must("create", "agent", "dev-code", "-project", "be98,zeeos")
	agentCode := linkRE.FindStringSubmatch(agent)
	if agentCode == nil || tokenRE.MatchString(agent) || !strings.Contains(agent, "curl -fsSL 'https://cafard.example/join/") ||
		!strings.Contains(agent, "irm 'https://cafard.example/join/") {
		t.Fatalf("create agent printed:\n%s", agent)
	}
	if out, code := b.admin("token", "dev-code"); code != 1 || !strings.Contains(out, "never shown") {
		t.Errorf("token for an agent: %d %s", code, out)
	}

	b.must("add", "zeeos", "camille")
	if out, code := b.admin("add", "zeeos", "camille"); code != 1 || !strings.Contains(out, "already a member") {
		t.Errorf("adding twice: %d %s", code, out)
	}
	if out, code := b.admin("create", "agent", "nowhere", "-project", "nope"); code != 1 || !strings.Contains(out, `no project "nope"`) {
		t.Errorf("an unknown project: %d %s", code, out)
	}
	if out, code := b.admin("frobnicate"); code != 2 || !strings.Contains(out, "unknown command") {
		t.Errorf("an unknown command: %d %s", code, out)
	}

	list := b.must("list")
	for _, want := range []string{"guillaume", "person", "dev-build", "device", "be98", "zeeos", "camille"} {
		if !strings.Contains(list, want) {
			t.Errorf("list lacks %q:\n%s", want, list)
		}
	}

	// The tokens work against the server, and a new one replaces the old.
	pub := b.serve()
	resp, err := http.Post("http://"+pub+"/join/"+agentCode[1]+"/token", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	agentToken := strings.TrimSpace(string(raw))
	if resp.StatusCode != 200 || !strings.HasPrefix(agentToken, "cfd_ag_") {
		t.Fatalf("redeeming the install link: %d %q", resp.StatusCode, raw)
	}
	send := func(token, path, body string) int {
		req, _ := http.NewRequest("PUT", "http://"+pub+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := send(devToken, "/api/v1/send/be98/first/boot.log", "hello\n"); code != 201 {
		t.Errorf("device send: %d", code)
	}
	newer := tokenRE.FindString(b.must("token", "dev-build"))
	if code := send(devToken, "/api/v1/send/be98/first/boot.log", "again\n"); code != 401 {
		t.Errorf("the replaced token: %d, want 401", code)
	}
	if code := send(newer, "/api/v1/send/be98/first/boot.log", "again\n"); code != 201 {
		t.Errorf("the new token: %d", code)
	}
	req, _ := http.NewRequest("GET", "http://"+pub+"/api/v1/projects/be98/conversations/first/messages", nil)
	req.Header.Set("Authorization", "Bearer "+agentToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || strings.Count(string(body), `"boot.log"`) != 2 {
		t.Errorf("agent reads: %d %s", resp.StatusCode, body)
	}

	if out := b.must("list"); !strings.Contains(out, "from 127.0.0.1") {
		t.Errorf("list does not show the token's last use:\n%s", out)
	}
}

func TestAdminNeedsTheStateDirectory(t *testing.T) {
	b := newBox(t)
	os.RemoveAll(b.cfg.StateDir)
	if out, code := b.admin("list"); code != 1 || !strings.Contains(out, "start cafard once first") {
		t.Errorf("no state directory: %d %s", code, out)
	}
	if _, err := os.Stat(b.cfg.StateDir); err == nil {
		t.Error("cafard admin created the state directory")
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"500M": 500 << 20, "5G": 5 << 30, "5GB": 5 << 30, "1t": 1 << 40, "1024": 1024} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "five", "-1G", "0"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) accepted", bad)
		}
	}
}

// serve starts the server on the box and returns the public address.
func (b *box) serve() string {
	b.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- runServer(ctx, b.cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), func(p, _ net.Addr) { addrs <- p })
	}()
	b.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			b.t.Error("server did not stop")
		}
	})
	select {
	case a := <-addrs:
		return a.String()
	case err := <-done:
		b.t.Fatalf("server stopped: %v", err)
	case <-time.After(10 * time.Second):
		b.t.Fatal("server did not start")
	}
	return ""
}

// passwd writes the hash into the configuration, in place of the example's
// comment, and the server's check accepts the password.
func TestPasswd(t *testing.T) {
	b := newBox(t)
	os.WriteFile(b.path, append(must(os.ReadFile(b.path)), []byte("# admin-password-hash: \"$argon2id$…\"\n")...), 0o600)
	passwd := func(input string) (string, int) {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "stdin")
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(input)
		f.Seek(0, 0)
		defer f.Close()
		var out, errOut strings.Builder
		code := passwdCommand([]string{"-config", b.path}, f, &out, &errOut)
		return out.String() + errOut.String(), code
	}
	if out, code := passwd("short\n"); code != 1 || !strings.Contains(out, "at least") {
		t.Errorf("a short password: %d %s", code, out)
	}
	if out, code := passwd("Admin000\n"); code != 0 || !strings.Contains(out, "Restart cafard") {
		t.Fatalf("passwd: %d %s", code, out)
	}
	cfg, err := config.Load(b.path)
	if err != nil {
		t.Fatal(err)
	}
	if !access.CheckAdminPassword(cfg.AdminPasswordHash, "Admin000") || access.CheckAdminPassword(cfg.AdminPasswordHash, "Admin001") {
		t.Errorf("the hash written does not check: %q", cfg.AdminPasswordHash)
	}
	if n := strings.Count(string(must(os.ReadFile(b.path))), "admin-password-hash"); n != 1 {
		t.Errorf("%d admin-password-hash lines", n)
	}
	// Again: the line is replaced, not added.
	passwd("another password\n")
	if n := strings.Count(string(must(os.ReadFile(b.path))), "admin-password-hash"); n != 1 {
		t.Errorf("after a second passwd, %d admin-password-hash lines", n)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
