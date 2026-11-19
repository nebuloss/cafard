package projects

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/store"
)

// SerialPort is a sink's serial port, and one entry of the old `serial:`
// config list (internal/ingest/serial calls it Port). It lives here
// because the serial package imports deps, which holds this service.
type SerialPort struct {
	// Name is the sink, "project/sink"; in the old config list, a bare
	// name that the import turns into a sink of the project imported.
	Name    string `yaml:"name"`
	Device  string `yaml:"device,omitempty"`  // local port: /dev/serial/by-id/..., COM5
	RFC2217 string `yaml:"rfc2217,omitempty"` // host:port of a Telnet COM Port Control server
	TCP     string `yaml:"tcp,omitempty"`     // host:port of a raw TCP serial server

	Baud     int     `yaml:"baud,omitempty"`
	DataBits int     `yaml:"data_bits,omitempty"`
	Parity   string  `yaml:"parity,omitempty"` // none | even | odd | mark | space
	StopBits float64 `yaml:"stop_bits,omitempty"`
	// Writable lets agents with a serial_write grant and the claim write
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
func (p SerialPort) Stream() string { return "serial:" + p.Name }

// Transport is local, rfc2217 or tcp.
func (p SerialPort) Transport() string {
	switch {
	case p.Device != "":
		return TransportLocal
	case p.RFC2217 != "":
		return TransportRFC2217
	}
	return TransportTCP
}

// Address is the device path or host:port.
func (p SerialPort) Address() string {
	return p.Device + p.RFC2217 + p.TCP
}

// Framing is "115200 8N1", or "" for raw TCP.
func (p SerialPort) Framing() string {
	if p.Transport() == TransportTCP || p.Parity == "" {
		return ""
	}
	stop := strconv.FormatFloat(p.StopBits, 'f', -1, 64)
	return fmt.Sprintf("%d %d%s%s", p.Baud, p.DataBits, strings.ToUpper(p.Parity[:1]), stop)
}

// SetDefaults fills 8N1 at 115200 baud where a local or RFC 2217 port
// leaves them unset.
func (p *SerialPort) SetDefaults() {
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

// Validate checks the address and the line settings; the name is checked
// by whoever gives it (a sink, or the config list).
func (p *SerialPort) Validate() error {
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
