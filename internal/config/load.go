package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"sort"

	"gopkg.in/yaml.v3"
)

// Loader registers -config and one flag per scalar key on a FlagSet, then
// builds the effective Config once the flags are parsed.
type Loader struct {
	path  string
	flags map[string]*flagValue // by key name, only the ones set on the command line
	keys  []key
}

// NewLoader registers the configuration flags on set.
func NewLoader(set *flag.FlagSet) *Loader {
	l := &Loader{flags: map[string]*flagValue{}, keys: scalarKeys()}
	set.StringVar(&l.path, "config", "", "configuration file (default "+DefaultPath+", or $CAFARD_CONFIG)")
	for _, k := range l.keys {
		fv := &flagValue{key: k, loader: l}
		set.Var(fv, k.flagName(), "sets "+k.name+" (env "+k.envName()+")")
	}
	return l
}

// Path is the file Load reads, or would read.
func (l *Loader) Path(getenv func(string) string) (path string, explicit bool) {
	if l.path != "" {
		return l.path, true
	}
	if p := getenv("CAFARD_CONFIG"); p != "" {
		return p, true
	}
	return DefaultPath, false
}

// Load reads the file, applies the environment and then the flags, and
// validates the result. It also returns the path it read, or "" when no file
// was found at the default location.
func (l *Loader) Load(getenv func(string) string) (*Config, string, error) {
	cfg := Default()
	path, explicit := l.Path(getenv)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := decodeYAML(data, &cfg); err != nil {
			return nil, path, fmt.Errorf("%s: %w", path, err)
		}
	case errors.Is(err, fs.ErrNotExist) && !explicit:
		path = ""
	default:
		return nil, path, err
	}

	root := reflect.ValueOf(&cfg).Elem()
	for _, k := range l.keys {
		raw := getenv(k.envName())
		if raw == "" {
			continue
		}
		if err := setString(root.FieldByIndex(k.index), raw); err != nil {
			return nil, path, fmt.Errorf("%s: %w", k.envName(), err)
		}
	}
	names := make([]string, 0, len(l.flags))
	for name := range l.flags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fv := l.flags[name]
		if err := setString(root.FieldByIndex(fv.key.index), fv.raw); err != nil {
			return nil, path, fmt.Errorf("-%s: %w", fv.key.flagName(), err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, path, err
	}
	return &cfg, path, nil
}

func decodeYAML(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Marshal renders the configuration as YAML, secrets redacted.
func (c *Config) Marshal() ([]byte, error) {
	r := c.Redacted()
	return yaml.Marshal(&r)
}

// flagValue checks a flag's syntax when it is parsed and keeps the raw text,
// so it can be applied after the file and the environment.
type flagValue struct {
	key    key
	loader *Loader
	raw    string
}

func (f *flagValue) String() string { return f.raw }

func (f *flagValue) Set(s string) error {
	probe := reflect.New(f.key.typ).Elem()
	if err := setString(probe, s); err != nil {
		return err
	}
	f.raw = s
	f.loader.flags[f.key.name] = f
	return nil
}

func (f *flagValue) IsBoolFlag() bool { return f.key.typ.Kind() == reflect.Bool }
