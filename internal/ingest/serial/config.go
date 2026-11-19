package serial

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/nebuloss/cafard/internal/projects"
)

// Port is one entry of the `serial:` list, and a sink's port.
type Port = projects.SerialPort

// Transport names, as status.Source.Transport.
const (
	TransportLocal   = projects.TransportLocal
	TransportRFC2217 = projects.TransportRFC2217
	TransportTCP     = projects.TransportTCP
)

// DefaultBaud is used when a local or RFC 2217 port sets none.
const DefaultBaud = projects.DefaultBaud

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// Config is the `serial:` section, a list of ports. It is a struct so the
// config package's walk finds SetDefaults and Validate on it; in YAML it is
// the plain list. It is deprecated: at start the ports are imported once
// into sinks of the project imported, which run them from then on.
type Config struct {
	Ports []Port `yaml:"-"`
}

// SetDefaults fills 8N1 at 115200 baud where a port leaves them unset.
func (c *Config) SetDefaults() {
	for i := range c.Ports {
		c.Ports[i].SetDefaults()
	}
}

// Validate checks every port and that names are unique.
func (c *Config) Validate() error { return c.validate(false) }

// validate also takes "project/sink" names when inProject.
func (c *Config) validate(inProject bool) error {
	seen := map[string]bool{}
	for i := range c.Ports {
		p := &c.Ports[i]
		name := p.Name
		if project, sink, ok := projects.SplitRef(name); ok && inProject && projects.ValidateName(project) == nil {
			name = sink
		}
		if !nameRE.MatchString(name) {
			return fmt.Errorf("name %q must be 1 to 63 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit", p.Name)
		}
		if err := p.Validate(); err != nil {
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
