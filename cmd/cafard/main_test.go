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
	"strings"
	"testing"
	"time"

	"cafard/internal/config"
	"cafard/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Offline(m) }

func TestVersionAndUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "cafard dev") {
		t.Errorf("version: %d %q", code, out.String())
	}
	out.Reset()
	if code := run(nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("no command: %d %q", code, errOut.String())
	}
	errOut.Reset()
	if code := run([]string{"frobnicate"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), `unknown command "frobnicate"`) {
		t.Errorf("unknown command: %d %q", code, errOut.String())
	}
}

func TestServeRefusesAnOverlappingAdminListener(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("listen: 127.0.0.1:18330\nadmin-listen: 127.0.0.1:18330\nstate-dir: "+t.TempDir()+"\n"), 0o600)
	var errOut bytes.Buffer
	if code := run([]string{"serve", "-config", p}, io.Discard, &errOut); code != 1 || !strings.Contains(errOut.String(), "overlap") {
		t.Errorf("serve with overlapping listeners: %d %q", code, errOut.String())
	}
}

func TestConfigPrintsWhatIsInEffect(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("listen: 0.0.0.0:8790\nadmin-listen: 0.0.0.0:8791\n"), 0o600)
	var out, errOut bytes.Buffer
	if code := run([]string{"config", "-config", p}, &out, &errOut); code != 0 {
		t.Fatalf("config: %d %q", code, errOut.String())
	}
	for _, want := range []string{"listen: 0.0.0.0:8790", "admin-listen: 0.0.0.0:8791", "state-dir: /var/lib/cafard", "level: info"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("config output lacks %q:\n%s", want, out.String())
		}
	}

	os.WriteFile(p, []byte("listen: 0.0.0.0:8790\nadmin-listen: 0.0.0.0:8790\n"), 0o600)
	errOut.Reset()
	if code := run([]string{"config", "-config", p}, io.Discard, &errOut); code != 1 || !strings.Contains(errOut.String(), "overlap") {
		t.Errorf("config with overlapping listeners: %d %q", code, errOut.String())
	}
}

// The whole server, on two ephemeral ports: the state directory is created
// private, both listeners answer, the public one refuses /admin, and
// cancelling stops both.
func TestServerRunsBothListenersAndStops(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	cfg := config.Default()
	cfg.Listen, cfg.AdminListen, cfg.StateDir = "127.0.0.1:0", "127.0.0.1:0", state

	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan [2]net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- runServer(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), func(p, a net.Addr) { addrs <- [2]net.Addr{p, a} })
	}()
	var got [2]net.Addr
	select {
	case got = <-addrs:
	case err := <-done:
		t.Fatalf("server stopped before listening: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}

	status := func(addr net.Addr, path string) int {
		resp, err := http.Get("http://" + addr.String() + path)
		if err != nil {
			t.Fatalf("GET %s%s: %v", addr, path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := status(got[0], "/healthz"); c != 200 {
		t.Errorf("public /healthz: %d", c)
	}
	if c := status(got[1], "/healthz"); c != 200 {
		t.Errorf("admin /healthz: %d", c)
	}
	if c := status(got[0], "/admin/api/projects"); c != 404 {
		t.Errorf("public /admin/api/projects: %d, want 404", c)
	}

	if st, err := os.Stat(state); err != nil {
		t.Errorf("state directory: %v", err)
	} else if st.Mode().Perm() != 0o700 {
		t.Errorf("state directory mode %v, want 0700", st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(state, "cafard.db")); err != nil {
		t.Errorf("no database in the state directory: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("stop: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not stop")
	}
}
