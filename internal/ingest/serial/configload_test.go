package serial_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/config"
)

func load(t *testing.T, yaml string) (*config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cafard.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	l := config.NewLoader(flag.NewFlagSet("t", flag.ContinueOnError))
	cfg, _, err := l.Load(func(k string) string {
		if k == "CAFARD_CONFIG" {
			return path
		}
		return ""
	})
	return cfg, err
}

func TestTheConfigFileCarriesTheSerialList(t *testing.T) {
	cfg, err := load(t, "serial:\n  - name: dut1\n    device: /dev/ttyUSB0\n  - name: dut3\n    tcp: 10.0.50.30:7002\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Serial.Ports) != 2 || cfg.Serial.Ports[0].Baud != 115200 {
		t.Fatalf("%+v", cfg.Serial.Ports)
	}
	out, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "device: /dev/ttyUSB0") || !strings.Contains(string(out), "baud: 115200") {
		t.Errorf("printed config:\n%s", out)
	}

	if _, err := load(t, "serial:\n  - name: dut1\n    device: /dev/ttyUSB0\n    tcp: h:1\n"); err == nil || !strings.HasPrefix(err.Error(), "serial: ") {
		t.Errorf("two transports: %v", err)
	}
	if _, err := load(t, "serial:\n  - name: dut1\n    devise: /dev/ttyUSB0\n"); err == nil || !strings.Contains(err.Error(), "devise") {
		t.Errorf("unknown key: %v", err)
	}
	// No serial section: nothing to run, and no flag was made for it.
	cfg, err = load(t, "")
	if err != nil || cfg.Serial.Ports != nil {
		t.Fatalf("%v %+v", err, cfg)
	}
	set := flag.NewFlagSet("t", flag.ContinueOnError)
	config.NewLoader(set)
	set.VisitAll(func(f *flag.Flag) {
		if strings.HasPrefix(f.Name, "serial") {
			t.Errorf("flag -%s exists; serial is file-only", f.Name)
		}
	})
}
