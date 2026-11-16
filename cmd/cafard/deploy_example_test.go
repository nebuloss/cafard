package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/nebuloss/cafard/internal/config"
)

const exampleConfig = "../../deploy/cafard.yaml.example"

// The installer copies the example to /etc/cafard.yaml and runs
// `cafard config` before starting, so a key the loader does not know would
// stop a fresh install from starting at all.
func TestDeployExampleConfigLoads(t *testing.T) {
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	loader := config.NewLoader(set)
	if err := set.Parse([]string{"-config", exampleConfig}); err != nil {
		t.Fatal(err)
	}
	cfg, path, err := loader.Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("the example config does not load: %v", err)
	}
	if path != exampleConfig {
		t.Fatalf("loaded %q, want %q", path, exampleConfig)
	}
	// The example documents the defaults; it must not change them.
	want := config.Default()
	got, _ := cfg.Marshal()
	def, _ := want.Marshal()
	if string(got) != string(def) {
		t.Errorf("the example config differs from the defaults:\n--- example\n%s\n--- defaults\n%s", got, def)
	}
}

// Every key cafard knows is mentioned in the example, commented or not, so
// the file stays the reference for what can be set.
func TestDeployExampleConfigMentionsEveryKey(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(exampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	def := config.Default()
	out, err := def.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := yaml.Unmarshal(out, &tree); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k, v := range tree {
		keys = append(keys, k)
		if sub, ok := v.(map[string]any); ok {
			for sk := range sub {
				keys = append(keys, sk)
			}
		}
	}
	for _, k := range keys {
		re := regexp.MustCompile(`(?m)^[ \t]*(# ?)?[ \t]*` + regexp.QuoteMeta(k) + `:`)
		if !re.Match(raw) {
			t.Errorf("key %q is not in %s", k, exampleConfig)
		}
	}
}
