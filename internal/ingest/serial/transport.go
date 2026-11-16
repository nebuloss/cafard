package serial

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	bugserial "go.bug.st/serial"

	"github.com/nebuloss/cafard/internal/ingest/serial/rfc2217"
	"github.com/nebuloss/cafard/internal/status"
)

// Events is how an open link reports what happens on the line.
type Events interface {
	// Marker writes a marker entry into the port's stream.
	Marker(text string, severity int)
	// LineState merges the known fields into the port's line state.
	LineState(ls status.LineState)
}

// Factory opens a port's transport. The server uses Open; tests pass
// pipes. A link that can drive the lines also implements Controller.
type Factory func(ctx context.Context, p Port, ev Events) (io.ReadWriteCloser, error)

// Controller is a link that drives DTR, RTS, break and the baud rate.
type Controller interface {
	SetDTR(ctx context.Context, on bool) error
	SetRTS(ctx context.Context, on bool) error
	Break(ctx context.Context, d time.Duration) error
	SetBaud(ctx context.Context, baud int) error
}

// Optional link methods.
type (
	// describer gives the text of the "connected" marker.
	describer interface{ Describe() string }
	// controlRefuser explains why a Controller cannot be used right now
	// (an RFC 2217 peer that refused the option); "" means it can.
	controlRefuser interface{ ControlUnavailable() string }
	// modemReader reads CTS and DCD.
	modemReader interface{ Modem() status.LineState }
)

const dialTimeout = 10 * time.Second

// Open is the real Factory: a local port, raw TCP, or RFC 2217.
func Open(ctx context.Context, p Port, ev Events) (io.ReadWriteCloser, error) {
	switch p.Transport() {
	case TransportLocal:
		l, err := OpenLocal(p)
		if err != nil {
			return nil, err
		}
		ev.LineState(l.initial)
		ls := l.Modem()
		ev.LineState(ls)
		return l, nil
	case TransportTCP:
		d := net.Dialer{Timeout: dialTimeout}
		c, err := d.DialContext(ctx, "tcp", p.TCP)
		if err != nil {
			return nil, err
		}
		return &tcpLink{Conn: c, addr: p.TCP}, nil
	}
	d := net.Dialer{Timeout: dialTimeout}
	c, err := d.DialContext(ctx, "tcp", p.RFC2217)
	if err != nil {
		return nil, err
	}
	return newRFC2217(ctx, c, p, ev)
}

// LocalPort is an open local serial port.
type LocalPort struct {
	port    bugserial.Port
	mode    bugserial.Mode
	name    string
	mu      sync.Mutex
	initial status.LineState
}

// OpenLocalRW is OpenLocal as an io.ReadWriteCloser, nil on error rather
// than a typed nil, for callers that only read and write.
func OpenLocalRW(p Port) (io.ReadWriteCloser, error) {
	l, err := OpenLocal(p)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// OpenLocal opens p.Device with p's line settings. DTR and RTS come up
// asserted, as with any terminal program.
func OpenLocal(p Port) (*LocalPort, error) {
	p.setDefaults()
	mode := bugserial.Mode{BaudRate: p.Baud, DataBits: p.DataBits}
	switch p.Parity {
	case "odd":
		mode.Parity = bugserial.OddParity
	case "even":
		mode.Parity = bugserial.EvenParity
	case "mark":
		mode.Parity = bugserial.MarkParity
	case "space":
		mode.Parity = bugserial.SpaceParity
	}
	switch p.StopBits {
	case 1.5:
		mode.StopBits = bugserial.OnePointFiveStopBits
	case 2:
		mode.StopBits = bugserial.TwoStopBits
	}
	port, err := bugserial.Open(p.Device, &mode)
	if err != nil {
		return nil, err
	}
	on := true
	return &LocalPort{port: port, mode: mode, name: fmt.Sprintf("%s at %s", p.Device, p.Framing()),
		initial: status.LineState{DTR: &on, RTS: &on}}, nil
}

func (l *LocalPort) Read(b []byte) (int, error)  { return l.port.Read(b) }
func (l *LocalPort) Write(b []byte) (int, error) { return l.port.Write(b) }
func (l *LocalPort) Close() error                { return l.port.Close() }
func (l *LocalPort) Describe() string            { return "local port " + l.name }

func (l *LocalPort) SetDTR(_ context.Context, on bool) error { return l.port.SetDTR(on) }
func (l *LocalPort) SetRTS(_ context.Context, on bool) error { return l.port.SetRTS(on) }

func (l *LocalPort) Break(_ context.Context, d time.Duration) error { return l.port.Break(d) }

func (l *LocalPort) SetBaud(_ context.Context, baud int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.mode
	m.BaudRate = baud
	if err := l.port.SetMode(&m); err != nil {
		return err
	}
	l.mode = m
	return nil
}

// Modem reads CTS and DCD; both stay unknown if the driver cannot say.
func (l *LocalPort) Modem() status.LineState {
	bits, err := l.port.GetModemStatusBits()
	if err != nil || bits == nil {
		return status.LineState{}
	}
	cts, dcd := bits.CTS, bits.DCD
	return status.LineState{CTS: &cts, DCD: &dcd}
}

type tcpLink struct {
	net.Conn
	addr string
}

func (t *tcpLink) Describe() string {
	return "raw TCP " + t.addr + " (line settings fixed on the far end)"
}

// rfc2217Port is a port reached through an RFC 2217 server.
type rfc2217Port struct {
	*rfc2217.Conn
	port Port
}

// newRFC2217 negotiates COM-PORT-OPTION on conn and sends p's line
// settings. A peer that refuses is kept as plain Telnet.
func newRFC2217(ctx context.Context, conn io.ReadWriteCloser, p Port, ev Events) (*rfc2217Port, error) {
	p.setDefaults()
	var parity byte
	switch p.Parity {
	case "none":
		parity = rfc2217.ParityNone
	case "odd":
		parity = rfc2217.ParityOdd
	case "even":
		parity = rfc2217.ParityEven
	case "mark":
		parity = rfc2217.ParityMark
	case "space":
		parity = rfc2217.ParitySpace
	}
	stop := byte(rfc2217.StopOne)
	switch p.StopBits {
	case 1.5:
		stop = rfc2217.StopOneHalf
	case 2:
		stop = rfc2217.StopTwo
	}
	var (
		mu      sync.Mutex
		lastDCD *bool
	)
	c := rfc2217.New(conn, rfc2217.Options{
		Baud: p.Baud, DataBits: p.DataBits, Parity: parity, StopBits: stop,
		OnModemState: func(s byte) {
			cts, dcd := s&rfc2217.ModemCTS != 0, s&rfc2217.ModemDCD != 0
			ev.LineState(status.LineState{CTS: &cts, DCD: &dcd})
			mu.Lock()
			changed := lastDCD != nil && *lastDCD != dcd
			lastDCD = &dcd
			mu.Unlock()
			if changed {
				if dcd {
					ev.Marker("carrier detect (DCD) is up", 5)
				} else {
					ev.Marker("carrier detect (DCD) dropped", 4)
				}
			}
		},
	})
	if err := c.Negotiate(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return &rfc2217Port{Conn: c, port: p}, nil
}

// Describe says whether line settings come from cafard.
func (r *rfc2217Port) Describe() string {
	if ok, reason := r.ComPort(); !ok {
		return fmt.Sprintf("plain Telnet %s: %s, so baud, framing, DTR, RTS and break stay as set on the far end", r.port.RFC2217, reason)
	}
	return fmt.Sprintf("RFC 2217 %s at %s", r.port.RFC2217, r.port.Framing())
}

// ControlUnavailable is why line control is refused, or "".
func (r *rfc2217Port) ControlUnavailable() string {
	if ok, reason := r.ComPort(); !ok {
		return reason + ", so it is plain Telnet without line control"
	}
	return ""
}

func (r *rfc2217Port) control(ctx context.Context, v byte, what string) error {
	got, err := r.SetControl(ctx, v)
	if err != nil {
		return err
	}
	if got != v {
		return fmt.Errorf("the RFC 2217 server answered SET-CONTROL %d to %s (asked %d)", got, what, v)
	}
	return nil
}

func (r *rfc2217Port) SetDTR(ctx context.Context, on bool) error {
	if on {
		return r.control(ctx, rfc2217.ControlDTROn, "DTR on")
	}
	return r.control(ctx, rfc2217.ControlDTROff, "DTR off")
}

func (r *rfc2217Port) SetRTS(ctx context.Context, on bool) error {
	if on {
		return r.control(ctx, rfc2217.ControlRTSOn, "RTS on")
	}
	return r.control(ctx, rfc2217.ControlRTSOff, "RTS off")
}

func (r *rfc2217Port) Break(ctx context.Context, d time.Duration) error {
	if err := r.control(ctx, rfc2217.ControlBreakOn, "break on"); err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	// Break off even if ctx ended: a line left in break is a dead console.
	return r.control(context.WithoutCancel(ctx), rfc2217.ControlBreakOff, "break off")
}

func (r *rfc2217Port) SetBaud(ctx context.Context, baud int) error {
	got, err := r.Conn.SetBaud(ctx, baud)
	if err != nil {
		return err
	}
	if got != baud {
		return fmt.Errorf("the RFC 2217 server set %d baud instead of %d", got, baud)
	}
	return nil
}
