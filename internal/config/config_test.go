package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsAreValidAndLoopback(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{c.Listen, c.AdminListen} {
		if !strings.HasPrefix(a, "127.0.0.1:") {
			t.Errorf("default listener %s is not loopback: a first run must expose nothing", a)
		}
	}
}

// The committed example is the one copy of every key; it has to load.
func TestExampleLoads(t *testing.T) {
	if _, err := Load(filepath.Join("..", "..", "configs", "config.example.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestFileOverridesDefaults(t *testing.T) {
	c, err := Load(write(t, "listen: 0.0.0.0:9000\nadmin-listen: 10.0.0.1:9001\nstate-dir: /tmp/x\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "0.0.0.0:9000" || c.AdminListen != "10.0.0.1:9001" || c.StateDir != "/tmp/x" {
		t.Errorf("got %+v", c)
	}
	if c.Log.Level != "info" {
		t.Errorf("a key the file leaves out keeps its default: log.level = %q", c.Log.Level)
	}
}

func TestUnknownKeyIsAnError(t *testing.T) {
	_, err := Load(write(t, "admin_listen: 127.0.0.1:9\n"))
	if err == nil {
		t.Fatal("a misspelt key was accepted")
	}
}

func TestAdminListenerNeverOverlapsThePublicOne(t *testing.T) {
	for _, tc := range []struct {
		listen, admin string
		ok            bool
	}{
		{"127.0.0.1:8330", "127.0.0.1:8331", true},
		{"0.0.0.0:8330", "10.0.50.10:8331", true},
		{"10.0.50.10:8330", "127.0.0.1:8330", true}, // same port, different interfaces
		{"127.0.0.1:8330", "127.0.0.1:8330", false},
		{"0.0.0.0:8330", "127.0.0.1:8330", false}, // the wildcard covers loopback
		{":8330", "10.0.50.10:8330", false},
		{"127.0.0.1:8330", "", false},
		{"127.0.0.1", "127.0.0.1:8331", false},
	} {
		c := Default()
		c.Listen, c.AdminListen = tc.listen, tc.admin
		err := c.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("listen %q admin-listen %q: err = %v, want ok = %v", tc.listen, tc.admin, err, tc.ok)
		}
	}
}

func TestURL(t *testing.T) {
	c := Default()
	if got := c.URL(); got != "http://127.0.0.1:8330" {
		t.Errorf("URL() = %q", got)
	}
	c.PublicURL = "https://cafard.example"
	if got := c.URL(); got != "https://cafard.example" {
		t.Errorf("URL() = %q", got)
	}
}
