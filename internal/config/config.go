// Package config reads cafard's configuration file.
//
// The file holds where cafard listens, where its state lives, and the hash
// of the admin password, and nothing else: people, agents, projects and
// every setting the admin UI changes live in the database, so that nothing
// needs a restart and a backup is the whole of an install.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the file, after defaults.
type Config struct {
	// Listen is the public listener, the one published in the DMZ: the chat
	// app, the HTTP API, the MCP server and join links.
	Listen string `yaml:"listen"`
	// AdminListen is the admin UI and API, kept on the internal network.
	//
	// It is required, and must differ from Listen. claudication lets one
	// listener serve both; cafard does not, because its whole access model
	// rests on the DMZ being unable to create a person, an agent or a project.
	// Two listeners make that a property of the binary rather than of a proxy
	// configured correctly in front of it.
	AdminListen string `yaml:"admin-listen"`
	// StateDir holds the database and the files. Never inside the config.
	StateDir string `yaml:"state-dir"`
	// PublicURL is the address people and agents reach the public listener
	// at, as it appears in join links and in the MCP config the join script
	// writes. Empty means http://<listen>. The admin UI's settings can set
	// another over it, kept in the database.
	PublicURL string `yaml:"public-url"`
	// TrustedProxies are the proxies in front of cafard, by address or
	// CIDR: their X-Forwarded-For and X-Forwarded-Proto are believed. Behind
	// a proxy without them, every client shares the proxy's address, and
	// with it one rate limit.
	TrustedProxies []string `yaml:"trusted-proxies"`
	// AdminPasswordHash opens the admin UI: argon2id, as `cafard-server passwd`
	// writes it. Empty, nobody can sign in to the admin UI; `cafard-server admin` on
	// the box still works.
	AdminPasswordHash string `yaml:"admin-password-hash,omitempty"`
	Log               Log    `yaml:"log"`
}

type Log struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json or text
}

// Default is what a missing file, or a missing key, means: both listeners on
// loopback, so a first run exposes nothing.
func Default() Config {
	return Config{
		Listen:      "127.0.0.1:8330",
		AdminListen: "127.0.0.1:8331",
		StateDir:    "/var/lib/cafard",
		Log:         Log{Level: "info", Format: "json"},
	}
}

// Load reads the file at path over the defaults. An empty path is the
// defaults alone. An unknown key is an error: a misspelt admin-listen
// silently ignored would leave the admin UI where it was not meant to be.
func Load(path string) (Config, error) {
	c := Default()
	if path == "" {
		return c, c.Validate()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	return c, c.Validate()
}

// Validate refuses a configuration that would serve, but not as meant.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("config: listen is empty")
	}
	if c.AdminListen == "" {
		return errors.New("config: admin-listen is empty: cafard always serves the admin UI on a listener of its own")
	}
	if c.StateDir == "" {
		return errors.New("config: state-dir is empty")
	}
	if c.AdminPasswordHash != "" && !strings.HasPrefix(c.AdminPasswordHash, "$argon2id$") {
		return errors.New("config: admin-password-hash is not an argon2id hash: set it with cafard-server passwd")
	}
	if c.PublicURL != "" {
		if _, err := CheckPublicURL(c.PublicURL); err != nil {
			return fmt.Errorf("config: public-url: %w", err)
		}
	}
	if _, err := c.Proxies(); err != nil {
		return err
	}
	pub, err := splitAddr("listen", c.Listen)
	if err != nil {
		return err
	}
	adm, err := splitAddr("admin-listen", c.AdminListen)
	if err != nil {
		return err
	}
	if pub.port == adm.port && (pub.host == adm.host || pub.wildcard() || adm.wildcard()) {
		return fmt.Errorf("config: listen %s and admin-listen %s overlap: the admin UI must not be reachable on the public listener",
			c.Listen, c.AdminListen)
	}
	return nil
}

type addr struct{ host, port string }

// wildcard is an address that listens on every interface, and so overlaps
// any other address on the same port.
func (a addr) wildcard() bool {
	return a.host == "" || a.host == "0.0.0.0" || a.host == "::"
}

func splitAddr(key, s string) (addr, error) {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return addr{}, fmt.Errorf("config: %s %q: %w", key, s, err)
	}
	if port == "" || port == "0" {
		return addr{}, fmt.Errorf("config: %s %q: give a port", key, s)
	}
	return addr{host: host, port: port}, nil
}

// Proxies is TrustedProxies, each a prefix: an address alone is a /32 or
// a /128.
func (c Config) Proxies() ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range c.TrustedProxies {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("config: trusted-proxies: %q is neither an address nor a CIDR", s)
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// CheckPublicURL reads a public URL as the file or the admin UI gives it:
// http or https, a host, and nothing after it, since cafard answers at the
// root of its listener. It returns it without a trailing slash.
func CheckPublicURL(s string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("%q is not a URL", s)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return "", fmt.Errorf("%q: start it with https:// or http://", s)
	case u.Host == "" || u.User != nil:
		return "", fmt.Errorf("%q: give the host, as https://cafard.example.com", s)
	case strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "":
		return "", fmt.Errorf("%q: give the host alone; cafard answers at the root of its address", s)
	}
	return u.Scheme + "://" + u.Host, nil
}

// URL is PublicURL, or the address the public listener answers on.
func (c Config) URL() string {
	if c.PublicURL != "" {
		return c.PublicURL
	}
	return "http://" + c.Listen
}
