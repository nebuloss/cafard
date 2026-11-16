package serial

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nebuloss/cafard/internal/store"
)

// Port is one entry of the `serial:` list.
type Port struct {
	Name    string `yaml:"name"`
	Device  string `yaml:"device,omitempty"`  // local port: /dev/serial/by-id/..., COM5
	RFC2217 string `yaml:"rfc2217,omitempty"` // host:port of a Telnet COM Port Control server
	TCP     string `yaml:"tcp,omitempty"`     // host:port of a raw TCP serial server

	Baud     int     `yaml:"baud,omitempty"`
	DataBits int     `yaml:"data_bits,omitempty"`
	Parity   string  `yaml:"parity,omitempty"` // none | even | odd | mark | space
	StopBits float64 `yaml:"stop_bits,omitempty"`
	// Writable lets agents with the serial_write scope and the claim write
	// and drive the lines.
	Writable bool `yaml:"writable"`
}

// Transport names, as status.Source.Transport.
const (
	TransportLocal   = "local"
	TransportRFC2217 = "rfc2217"
	TransportTCP     = "tcp"
)

// DefaultBaud is used when a local or RFC 2217 port sets none.
const DefaultBaud = 115200

// Stream is the port's stream id.
func (p Port) Stream() string { return "serial:" + p.Name }

// Transport is local, rfc2217 or tcp.
func (p Port) Transport() string {
	switch {
	case p.Device != "":
		return TransportLocal
	case p.RFC2217 != "":
		return TransportRFC2217
	}
	return TransportTCP
}

// Address is the device path or host:port.
func (p Port) Address() string {
	return p.Device + p.RFC2217 + p.TCP
}

// Framing is "115200 8N1", or "" for raw TCP.
func (p Port) Framing() string {
	if p.Transport() == TransportTCP || p.Parity == "" {
		return ""
	}
	stop := strconv.FormatFloat(p.StopBits, 'f', -1, 64)
	return fmt.Sprintf("%d %d%s%s", p.Baud, p.DataBits, strings.ToUpper(p.Parity[:1]), stop)
}

func (p *Port) setDefaults() {
	if p.Transport() == TransportTCP {
		return
	}
	if p.Baud == 0 {
		p.Baud = DefaultBaud
	}
	if p.DataBits == 0 {
		p.DataBits = 8
	}
	if p.Parity == "" {
		p.Parity = "none"
	}
	if p.StopBits == 0 {
		p.StopBits = 1
	}
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

func (p *Port) validate() error {
	if !nameRE.MatchString(p.Name) {
		return fmt.Errorf("name %q must be 1 to 63 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit", p.Name)
	}
	if err := store.ValidateStreamID(p.Stream()); err != nil {
		return err
	}
	n := 0
	for _, s := range []string{p.Device, p.RFC2217, p.TCP} {
		if s != "" {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("%s: set exactly one of device, rfc2217 and tcp", p.Name)
	}
	if p.Device == "" {
		addr := p.RFC2217 + p.TCP
		host, port, err := net.SplitHostPort(addr)
		if err != nil || host == "" {
			return fmt.Errorf("%s: %q is not host:port", p.Name, addr)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%s: %q has no valid port", p.Name, addr)
		}
	}
	if p.TCP != "" {
		if p.Baud != 0 || p.DataBits != 0 || p.Parity != "" || p.StopBits != 0 {
			return fmt.Errorf("%s: raw tcp cannot set baud, data_bits, parity or stop_bits; set them on the far end, or use rfc2217", p.Name)
		}
		return nil
	}
	if p.Baud < 50 || p.Baud > 20_000_000 {
		return fmt.Errorf("%s: baud %d is out of range", p.Name, p.Baud)
	}
	if p.DataBits < 5 || p.DataBits > 8 {
		return fmt.Errorf("%s: data_bits must be 5, 6, 7 or 8", p.Name)
	}
	switch p.Parity {
	case "none", "even", "odd", "mark", "space":
	default:
		return fmt.Errorf("%s: parity %q is not one of none, even, odd, mark, space", p.Name, p.Parity)
	}
	switch p.StopBits {
	case 1, 1.5, 2:
	default:
		return fmt.Errorf("%s: stop_bits must be 1, 1.5 or 2", p.Name)
	}
	return nil
}

// Config is the `serial:` section, a list of ports. It is a struct so the
// config package's walk finds SetDefaults and Validate on it; in YAML it is
// the plain list.
type Config struct {
	Ports []Port `yaml:"-"`
}

// SetDefaults fills 8N1 at 115200 baud where a port leaves them unset.
func (c *Config) SetDefaults() {
	for i := range c.Ports {
		c.Ports[i].setDefaults()
	}
}

// Validate checks every port and that names are unique.
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for i := range c.Ports {
		p := &c.Ports[i]
		if err := p.validate(); err != nil {
			return err
		}
		if seen[p.Name] {
			return fmt.Errorf("two ports are named %q", p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

var portKeys = map[string]bool{"name": true, "device": true, "rfc2217": true, "tcp": true, "baud": true,
	"data_bits": true, "parity": true, "stop_bits": true, "writable": true}

// UnmarshalYAML reads the list. A custom unmarshaler loses the decoder's
// unknown-key check, so it is done here.
func (c *Config) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		c.Ports = nil
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("line %d: serial must be a list of ports", n.Line)
	}
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			return fmt.Errorf("line %d: a serial port is a mapping (name: ..., device: ...)", item.Line)
		}
		for i := 0; i+1 < len(item.Content); i += 2 {
			if k := item.Content[i]; !portKeys[k.Value] {
				return fmt.Errorf("line %d: field %s not found in a serial port", k.Line, k.Value)
			}
		}
	}
	var ports []Port
	if err := n.Decode(&ports); err != nil {
		return err
	}
	if len(ports) == 0 {
		ports = nil
	}
	c.Ports = ports
	c.SetDefaults()
	return nil
}

// MarshalYAML writes the plain list.
func (c Config) MarshalYAML() (any, error) {
	if c.Ports == nil {
		return []Port{}, nil
	}
	return c.Ports, nil
}
