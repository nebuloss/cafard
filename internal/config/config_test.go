package config

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func load(t *testing.T, args []string, vars map[string]string) (*Config, string, error) {
	t.Helper()
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	l := NewLoader(set)
	if err := set.Parse(args); err != nil {
		return nil, "", err
	}
	return l.Load(env(vars))
}

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cafard.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPrecedenceIsFlagThenEnvThenFileThenDefault(t *testing.T) {
	path := writeFile(t, "http_listen: 127.0.0.1:1001\npublic_url: http://file\nlog_level: warn\n")
	cfg, got, err := load(t,
		[]string{"-config", path, "-http-listen", "127.0.0.1:1003"},
		map[string]string{"CAFARD_HTTP_LISTEN": "127.0.0.1:1002", "CAFARD_PUBLIC_URL": "http://env"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Errorf("path = %q, want %q", got, path)
	}
	if cfg.HTTPListen != "127.0.0.1:1003" {
		t.Errorf("flag lost: http_listen = %q", cfg.HTTPListen)
	}
	if cfg.PublicURL != "http://env" {
		t.Errorf("env lost: public_url = %q", cfg.PublicURL)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("file lost: log_level = %q", cfg.LogLevel)
	}
	if cfg.DataDir != Default().DataDir {
		t.Errorf("default lost: data_dir = %q", cfg.DataDir)
	}
}

func TestConfigPathFromEnvironment(t *testing.T) {
	path := writeFile(t, "data_dir: /srv/cafard\n")
	cfg, _, err := load(t, nil, map[string]string{"CAFARD_CONFIG": path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/srv/cafard" {
		t.Errorf("data_dir = %q", cfg.DataDir)
	}
}

func TestMissingExplicitFileIsAnError(t *testing.T) {
	_, _, err := load(t, []string{"-config", filepath.Join(t.TempDir(), "nope.yaml")}, nil)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not exist", err)
	}
}

func TestUnknownKeyIsAnError(t *testing.T) {
	path := writeFile(t, "http_lisen: 127.0.0.1:1\n")
	_, _, err := load(t, []string{"-config", path}, nil)
	if err == nil || !strings.Contains(err.Error(), "http_lisen") {
		t.Fatalf("err = %v, want it to name the unknown key", err)
	}
}

func TestEmptyFileIsDefaults(t *testing.T) {
	path := writeFile(t, "# nothing\n")
	cfg, _, err := load(t, []string{"-config", path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*cfg, Default()) {
		t.Errorf("cfg = %+v, want defaults", *cfg)
	}
}

func TestBoolFlagWithoutValue(t *testing.T) {
	path := writeFile(t, "")
	cfg, _, err := load(t, []string{"-config", path, "-secure"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Secure {
		t.Error("-secure did not set secure")
	}
}

func TestBadFlagValueFailsAtParse(t *testing.T) {
	_, _, err := load(t, []string{"-secure=maybe"}, nil)
	if err == nil {
		t.Fatal("want a parse error")
	}
}

func TestBadEnvValueNamesTheVariable(t *testing.T) {
	path := writeFile(t, "")
	_, _, err := load(t, []string{"-config", path}, map[string]string{"CAFARD_SECURE": "maybe"})
	if err == nil || !strings.Contains(err.Error(), "CAFARD_SECURE") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]func(*Config){
		"data_dir":    func(c *Config) { c.DataDir = "" },
		"http_listen": func(c *Config) { c.HTTPListen = "nope" },
		"public_url":  func(c *Config) { c.PublicURL = "ftp://x" },
		"tls_cert":    func(c *Config) { c.TLSCert = "/x.crt" },
		"log_level":   func(c *Config) { c.LogLevel = "loud" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := Default()
			mutate(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %v, want it to name %s", err, name)
			}
		})
	}
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
}

type feature struct {
	MaxBodyMiB int           `yaml:"max_body_mib"`
	Every      time.Duration `yaml:"every"`
	Token      string        `yaml:"token" secret:"true"`
	Allow      []string      `yaml:"allow"`
	Inner      inner         `yaml:"inner"`
}

type inner struct {
	Name string `yaml:"name"`
}

func (f *feature) Validate() error {
	if f.MaxBodyMiB < 0 {
		return errors.New("max_body_mib: must not be negative")
	}
	return nil
}

type root struct {
	Listen string  `yaml:"listen"`
	OTLP   feature `yaml:"otlp"`
	Skip   string  `yaml:"-"`
}

func TestNestedKeysGetFlagAndEnvNames(t *testing.T) {
	var keys []key
	walkType(reflect.TypeFor[root](), "", nil, &keys)
	got := map[string][2]string{}
	for _, k := range keys {
		got[k.name] = [2]string{k.flagName(), k.envName()}
	}
	want := map[string][2]string{
		"listen":            {"listen", "CAFARD_LISTEN"},
		"otlp.max_body_mib": {"otlp-max-body-mib", "CAFARD_OTLP_MAX_BODY_MIB"},
		"otlp.every":        {"otlp-every", "CAFARD_OTLP_EVERY"},
		"otlp.token":        {"otlp-token", "CAFARD_OTLP_TOKEN"},
		"otlp.inner.name":   {"otlp-inner-name", "CAFARD_OTLP_INNER_NAME"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v\nwant %v", got, want)
	}
}

func TestSetStringParsesDurations(t *testing.T) {
	var f feature
	if err := setString(reflect.ValueOf(&f).Elem().FieldByName("Every"), "336h"); err != nil {
		t.Fatal(err)
	}
	if f.Every != 336*time.Hour {
		t.Errorf("every = %v", f.Every)
	}
}

func TestFeatureValidatorIsCalledWithItsKey(t *testing.T) {
	r := root{OTLP: feature{MaxBodyMiB: -1}}
	err := validateStructs(reflect.ValueOf(&r).Elem())
	if err == nil || !strings.HasPrefix(err.Error(), "otlp: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedactDoesNotTouchTheOriginal(t *testing.T) {
	r := root{OTLP: feature{Token: "cfd_secret"}}
	cp := r
	redact(reflect.ValueOf(&cp).Elem())
	if cp.OTLP.Token != redactedText {
		t.Errorf("copy token = %q", cp.OTLP.Token)
	}
	if r.OTLP.Token != "cfd_secret" {
		t.Errorf("original token changed to %q", r.OTLP.Token)
	}
}

func TestMarshalRoundTrips(t *testing.T) {
	c := Default()
	c.PublicURL = "http://10.0.50.2:8790"
	out, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := decodeYAML(out, &back); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !reflect.DeepEqual(back, c) {
		t.Errorf("round trip = %+v, want %+v", back, c)
	}
}
