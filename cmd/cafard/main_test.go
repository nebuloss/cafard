package main

import (
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

	"github.com/nebuloss/cafard/internal/config"
)

func runArgs(t *testing.T, vars map[string]string, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut strings.Builder
	err := run(context.Background(), args, strings.NewReader(""), &out, &errOut, func(k string) string { return vars[k] })
	return out.String(), errOut.String(), err
}

func TestVersionPrintsDev(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		out, _, err := runArgs(t, nil, arg)
		if err != nil {
			t.Fatal(err)
		}
		if out != "cafard dev\n" {
			t.Errorf("%s: out = %q", arg, out)
		}
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if _, _, err := runArgs(t, nil, "frobnicate"); err == nil {
		t.Fatal("want an error")
	}
	if _, _, err := runArgs(t, nil); err == nil {
		t.Fatal("want an error with no command")
	}
}

func TestUnimplementedCommandsSaySo(t *testing.T) {
	_, _, err := runArgs(t, nil, "term")
	if err == nil || !strings.Contains(err.Error(), "M6") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigPrintsTheEffectiveConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cafard.yaml")
	if err := os.WriteFile(path, []byte("public_url: http://10.0.50.9:8790\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := runArgs(t, map[string]string{"CAFARD_LOG_LEVEL": "debug"}, "config", "-config", path, "-http-listen", "127.0.0.1:9")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# loaded from " + path, "public_url: http://10.0.50.9:8790", "http_listen: 127.0.0.1:9", "log_level: debug"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestConfigFailsOnAnInvalidConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cafard.yaml")
	if err := os.WriteFile(path, []byte("log_level: loud\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runArgs(t, nil, "config", "-config", path); err == nil {
		t.Fatal("want an error")
	}
}

func TestServeAnswersHealthzAndShutsDownGracefully(t *testing.T) {
	cfg := config.Default()
	cfg.HTTPListen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrs := make(chan net.Addr, 1)
	done := make(chan error, 1)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() { done <- serve(ctx, &cfg, log, io.Discard, func(a net.Addr) { addrs <- a }) }()

	var addr net.Addr
	select {
	case addr = <-addrs:
	case err := <-done:
		t.Fatalf("serve returned early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server never became ready")
	}

	resp, err := http.Get("http://" + addr.String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok dev\n" {
		t.Errorf("healthz = %d %q", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop")
	}
}
