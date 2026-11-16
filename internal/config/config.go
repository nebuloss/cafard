// Package config loads cafard's settings from /etc/cafard.yaml, the
// environment and flags, in that order of increasing precedence.
//
// Every scalar key gets a flag and an environment variable for free: the key
// otlp.max_body_mib is -otlp-max-body-mib and CAFARD_OTLP_MAX_BODY_MIB. Lists
// and maps (serial ports, channels, allowlists) are file-only, because a list
// squeezed into one flag is a list nobody can read back.
//
// A feature adds one field to Config, typed by a struct its own package
// defines. If that struct has a Validate() error method (pointer receiver), it
// is called after loading.
package config

import (
	"fmt"

	"github.com/nebuloss/cafard/internal/ingest/serial"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/store"
	"net"
	"net/url"
	"strconv"
)

// DefaultPath is where the service looks when neither -config nor
// CAFARD_CONFIG names a file. A missing file there is not an error.
const DefaultPath = "/etc/cafard.yaml"

// Config is everything cafard can be told.
type Config struct {
	DataDir    string `yaml:"data_dir"`
	HTTPListen string `yaml:"http_listen"`
	// PublicURL is the address phones and agents reach this server at. It
	// cannot be worked out from a request behind a reverse proxy.
	PublicURL string `yaml:"public_url"`
	Secure    bool   `yaml:"secure"`
	TLSCert   string `yaml:"tls_cert"`
	TLSKey    string `yaml:"tls_key"`
	LogLevel  string `yaml:"log_level"`

	Retention store.Retention  `yaml:"retention"`
	MCP       mcpserver.Config `yaml:"mcp"`
	Serial    serial.Config    `yaml:"serial"`
}

// Validator is implemented by feature sub-structs that check themselves.
type Validator interface {
	Validate() error
}

// Defaulter is implemented by feature sub-structs that have defaults.
type Defaulter interface {
	SetDefaults()
}

// Default is the configuration with nothing set.
func Default() Config {
	c := Config{
		DataDir:    "/var/lib/cafard",
		HTTPListen: "0.0.0.0:8790",
		LogLevel:   "info",
	}
	applyDefaults(&c)
	return c
}

// Validate checks the root keys, then every feature sub-struct.
func (c *Config) Validate() error {
	if c.DataDir == "" {
		return fmt.Errorf("data_dir: must be set")
	}
	if err := checkListen(c.HTTPListen); err != nil {
		return fmt.Errorf("http_listen: %w", err)
	}
	if c.PublicURL != "" {
		if err := checkPublicURL(c.PublicURL); err != nil {
			return fmt.Errorf("public_url: %w", err)
		}
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return fmt.Errorf("tls_cert and tls_key: set both or neither")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level: %q is not one of debug, info, warn, error", c.LogLevel)
	}
	return validateFeatures(c)
}

// TLS reports whether cafard terminates TLS itself.
func (c *Config) TLS() bool {
	return c.TLSCert != ""
}

func checkListen(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%q is not host:port: %w", address, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("%q has no valid port", address)
	}
	return nil
}

func checkPublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%q must start with http:// or https://", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%q has no host", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%q must be a plain scheme://host[:port][/path]", raw)
	}
	return nil
}
