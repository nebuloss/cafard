// Package rfc2217 is a client for the Telnet COM Port Control Option
// (RFC 2217), as served by ser2net with telnet(rfc2217): it sets the far
// port's baud rate and framing and drives DTR, RTS and break over TCP.
//
// A peer that refuses the option is read as plain Telnet: data flows, line
// control is refused with ErrNoComPort.
package rfc2217

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// COM-PORT-OPTION commands from client to server. The server answers a
// command with the same code plus ServerOffset.
const (
	CmdSetBaudRate      = 1
	CmdSetDataSize      = 2
	CmdSetParity        = 3
	CmdSetStopSize      = 4
	CmdSetControl       = 5
	CmdNotifyLineState  = 6
	CmdNotifyModemState = 7
	ServerOffset        = 100
)

// SET-CONTROL values.
const (
	ControlBreakOn  = 5
	ControlBreakOff = 6
	ControlDTROn    = 8
	ControlDTROff   = 9
	ControlRTSOn    = 11
	ControlRTSOff   = 12
)

// Parity values of SET-PARITY.
const (
	ParityNone  = 1
	ParityOdd   = 2
	ParityEven  = 3
	ParityMark  = 4
	ParitySpace = 5
)

// Stop size values of SET-STOPSIZE.
const (
	StopOne     = 1
	StopTwo     = 2
	StopOneHalf = 3
)

// Modem state bits of NOTIFY-MODEMSTATE.
const (
	ModemDCD      = 0x80
	ModemRI       = 0x40
	ModemDSR      = 0x20
	ModemCTS      = 0x10
	ModemDeltaDCD = 0x08
)

// ErrNoComPort is returned by line control on a peer that refused
// COM-PORT-OPTION.
var ErrNoComPort = errors.New("the peer refused RFC 2217 COM-PORT-OPTION, so it is plain Telnet without line control")

// Options are the line settings sent once the option is accepted. Zero
// values are not sent.
type Options struct {
	Baud     int
	DataBits int
	Parity   byte // ParityNone...
	StopBits byte // StopOne...

	// OnModemState gets every NOTIFY-MODEMSTATE value, from the reading
	// goroutine; it must not block.
	OnModemState func(state byte)
	// NegotiateTimeout bounds the wait for an answer to COM-PORT-OPTION;
	// default 5 s.
	NegotiateTimeout time.Duration
	// ReplyTimeout bounds the wait for the server's answer to a command;
	// default 2 s. A server that stays silent is assumed to have obeyed.
	ReplyTimeout time.Duration
}

type option struct{ on, asked bool }

type reply struct {
	cmd   byte
	value []byte
}

// Conn is a Telnet connection with COM-PORT-OPTION. Read returns data with
// every Telnet command removed; Write escapes IAC.
type Conn struct {
	rw   io.ReadWriteCloser
	opts Options

	wmu sync.Mutex // one writer at a time, data and commands alike

	mu       sync.Mutex
	us, him  [256]option
	decided  chan struct{}
	once     sync.Once
	comport  bool
	reason   string
	readErr  error
	ctlMu    sync.Mutex // one command waiting for its reply at a time
	replies  chan reply
	data     chan []byte
	cur      []byte
	done     chan struct{}
	stopOnce sync.Once
}

// New wraps rw and starts reading it. Call Negotiate next.
func New(rw io.ReadWriteCloser, opts Options) *Conn {
	if opts.NegotiateTimeout <= 0 {
		opts.NegotiateTimeout = 5 * time.Second
	}
	if opts.ReplyTimeout <= 0 {
		opts.ReplyTimeout = 2 * time.Second
	}
	c := &Conn{
		rw:      rw,
		opts:    opts,
		decided: make(chan struct{}),
		replies: make(chan reply, 16),
		// Up to 256 reads of 4 KiB wait for the reader before the
		// connection stops being read.
		data: make(chan []byte, 256),
		done: make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Negotiate offers COM-PORT-OPTION, BINARY and SUPPRESS-GO-AHEAD in both
// directions, waits for the peer's answer to COM-PORT-OPTION, and sends
// the line settings if it was accepted. A refusal is not an error; see
// ComPort.
func (c *Conn) Negotiate(ctx context.Context) error {
	c.mu.Lock()
	for _, o := range []byte{OptComPort, OptBinary, OptSGA} {
		c.us[o].asked = true
	}
	for _, o := range []byte{OptBinary, OptSGA} {
		c.him[o].asked = true
	}
	c.mu.Unlock()
	if err := c.writeRaw([]byte{
		IAC, WILL, OptComPort,
		IAC, WILL, OptBinary, IAC, DO, OptBinary,
		IAC, WILL, OptSGA, IAC, DO, OptSGA,
	}); err != nil {
		return err
	}
	t := time.NewTimer(c.opts.NegotiateTimeout)
	defer t.Stop()
	select {
	case <-c.decided:
	case <-t.C:
		c.decide(false, fmt.Sprintf("the peer did not answer COM-PORT-OPTION within %s", c.opts.NegotiateTimeout))
	case <-ctx.Done():
		return ctx.Err()
	}
	if ok, _ := c.ComPort(); !ok {
		return nil
	}
	var msgs []byte
	if c.opts.Baud > 0 {
		msgs = append(msgs, subneg(CmdSetBaudRate, baudBytes(c.opts.Baud))...)
	}
	if c.opts.DataBits > 0 {
		msgs = append(msgs, subneg(CmdSetDataSize, []byte{byte(c.opts.DataBits)})...)
	}
	if c.opts.Parity > 0 {
		msgs = append(msgs, subneg(CmdSetParity, []byte{c.opts.Parity})...)
	}
	if c.opts.StopBits > 0 {
		msgs = append(msgs, subneg(CmdSetStopSize, []byte{c.opts.StopBits})...)
	}
	if len(msgs) == 0 {
		return nil
	}
	return c.writeRaw(msgs)
}

// ComPort reports whether the peer accepted COM-PORT-OPTION, and why not.
func (c *Conn) ComPort() (ok bool, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.comport, c.reason
}

// Read returns data bytes, Telnet commands removed.
func (c *Conn) Read(p []byte) (int, error) {
	if len(c.cur) == 0 {
		b, ok := <-c.data
		if !ok {
			c.mu.Lock()
			defer c.mu.Unlock()
			return 0, c.readErr
		}
		c.cur = b
	}
	n := copy(p, c.cur)
	c.cur = c.cur[n:]
	return n, nil
}

// Write sends data, escaping IAC.
func (c *Conn) Write(p []byte) (int, error) {
	c.mu.Lock()
	bin := c.us[OptBinary].on
	c.mu.Unlock()
	if err := c.writeRaw(escape(p, bin)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close closes the connection, which also ends the reading goroutine.
func (c *Conn) Close() error {
	c.stopOnce.Do(func() { close(c.done) })
	return c.rw.Close()
}

// SetControl sends SET-CONTROL with value v and returns the value the
// server answered (v itself if it stayed silent).
func (c *Conn) SetControl(ctx context.Context, v byte) (byte, error) {
	got, err := c.command(ctx, CmdSetControl, []byte{v})
	if err != nil {
		return 0, err
	}
	if len(got) != 1 {
		return v, nil
	}
	return got[0], nil
}

// SetBaud sends SET-BAUDRATE and returns the rate the server answered.
func (c *Conn) SetBaud(ctx context.Context, baud int) (int, error) {
	got, err := c.command(ctx, CmdSetBaudRate, baudBytes(baud))
	if err != nil {
		return 0, err
	}
	if len(got) != 4 {
		return baud, nil
	}
	return int(binary.BigEndian.Uint32(got)), nil
}

func (c *Conn) command(ctx context.Context, cmd byte, value []byte) ([]byte, error) {
	if ok, _ := c.ComPort(); !ok {
		return nil, ErrNoComPort
	}
	c.ctlMu.Lock()
	defer c.ctlMu.Unlock()
	for len(c.replies) > 0 {
		<-c.replies // stale answers to commands that timed out
	}
	if err := c.writeRaw(subneg(cmd, value)); err != nil {
		return nil, err
	}
	t := time.NewTimer(c.opts.ReplyTimeout)
	defer t.Stop()
	for {
		select {
		case r := <-c.replies:
			if r.cmd == cmd+ServerOffset {
				return r.value, nil
			}
		case <-t.C:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func baudBytes(baud int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(baud))
	return b
}

// subneg is IAC SB COM-PORT-OPTION cmd value IAC SE, IAC in value doubled.
func subneg(cmd byte, value []byte) []byte {
	out := []byte{IAC, SB, OptComPort, cmd}
	for _, b := range value {
		out = append(out, b)
		if b == IAC {
			out = append(out, IAC)
		}
	}
	return append(out, IAC, SE)
}

func (c *Conn) writeRaw(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.rw.Write(b)
	return err
}

func (c *Conn) decide(ok bool, reason string) {
	c.once.Do(func() {
		c.mu.Lock()
		c.comport, c.reason = ok, reason
		c.mu.Unlock()
		close(c.decided)
	})
}

func (c *Conn) readLoop() {
	p := &parser{onSub: c.onSub}
	p.onVerb = func(verb, opt byte) {
		c.onVerb(verb, opt)
		// The mode can change in the middle of a read.
		c.mu.Lock()
		p.binary = c.him[OptBinary].on
		c.mu.Unlock()
	}
	buf := make([]byte, 4096)
	for {
		n, err := c.rw.Read(buf)
		if n > 0 {
			if out := p.feed(buf[:n], nil); len(out) > 0 {
				select {
				case c.data <- out:
				case <-c.done:
				}
			}
		}
		if err != nil {
			c.decide(false, "the connection closed during negotiation")
			c.mu.Lock()
			c.readErr = err
			c.mu.Unlock()
			close(c.data)
			return
		}
	}
}

// onVerb answers the peer's option requests. It replies only when the
// state changes, which is what keeps two Telnet ends from looping.
func (c *Conn) onVerb(verb, opt byte) {
	c.mu.Lock()
	var answer []byte
	switch verb {
	case DO, DONT:
		o := &c.us[opt]
		supported := opt == OptComPort || opt == OptBinary || opt == OptSGA
		switch {
		case verb == DO && !supported:
			answer = []byte{IAC, WONT, opt}
		case verb == DO && !o.on:
			if !o.asked {
				answer = []byte{IAC, WILL, opt}
			}
			o.on, o.asked = true, false
		case verb == DONT:
			if o.on {
				answer = []byte{IAC, WONT, opt}
			}
			o.on, o.asked = false, false
		}
	case WILL, WONT:
		o := &c.him[opt]
		supported := opt == OptBinary || opt == OptSGA
		switch {
		case verb == WILL && !supported:
			answer = []byte{IAC, DONT, opt}
		case verb == WILL && !o.on:
			if !o.asked {
				answer = []byte{IAC, DO, opt}
			}
			o.on, o.asked = true, false
		case verb == WONT:
			if o.on {
				answer = []byte{IAC, DONT, opt}
			}
			o.on, o.asked = false, false
		}
	}
	c.mu.Unlock()
	if answer != nil {
		_ = c.writeRaw(answer)
	}
	if opt == OptComPort && (verb == DO || verb == DONT) {
		if verb == DO {
			c.decide(true, "")
		} else {
			c.decide(false, "the peer answered DONT COM-PORT-OPTION")
		}
	}
}

func (c *Conn) onSub(body []byte) {
	if len(body) < 2 || body[0] != OptComPort {
		return
	}
	cmd, value := body[1], body[2:]
	switch cmd {
	case CmdNotifyModemState + ServerOffset:
		if len(value) == 1 && c.opts.OnModemState != nil {
			c.opts.OnModemState(value[0])
		}
	case CmdNotifyLineState + ServerOffset:
		// Overruns and framing errors show in the data; nothing to do.
	default:
		r := reply{cmd: cmd, value: append([]byte(nil), value...)}
		select {
		case c.replies <- r:
		default:
		}
	}
}
