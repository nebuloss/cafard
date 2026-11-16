package serial

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nebuloss/cafard/internal/status"
)

// Errors of Write and Control. Each comes wrapped with the stream and the
// reason, so its text can go to an agent as is.
var (
	ErrUnknownStream = errors.New("no serial port has this stream")
	ErrNotWritable   = errors.New("the port is not writable in the configuration")
	ErrDisconnected  = errors.New("the port is not connected")
	ErrUnsupported   = errors.New("the transport cannot do this")
	ErrBadRequest    = errors.New("invalid control request")
)

// UnsupportedError says why a port's transport cannot drive its lines.
type UnsupportedError struct {
	Stream    string
	Transport string
	Reason    string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%s (%s) cannot drive DTR, RTS, break or the baud rate: %s", e.Stream, e.Transport, e.Reason)
}

// Is makes errors.Is(err, ErrUnsupported) hold.
func (e *UnsupportedError) Is(target error) bool { return target == ErrUnsupported }

// MaxPulse bounds break_ms and pulse.ms.
const MaxPulse = 10 * time.Second

// ControlRequest is what serial_control asks for. Steps run in field
// order: baud, dtr, rts, break, pulse. An empty request only reads the
// line state.
type ControlRequest struct {
	DTR     *bool  `json:"dtr,omitempty"`
	RTS     *bool  `json:"rts,omitempty"`
	BreakMS int    `json:"break_ms,omitempty"`
	Pulse   *Pulse `json:"pulse,omitempty"`
	Baud    int    `json:"baud,omitempty"`
}

// Pulse flips a line for MS milliseconds and puts it back: the usual way
// to reset a board whose reset pin hangs on DTR or RTS.
type Pulse struct {
	Line string `json:"line"` // "dtr" | "rts"
	MS   int    `json:"ms"`
}

func (r ControlRequest) validate() error {
	if r.BreakMS < 0 || time.Duration(r.BreakMS)*time.Millisecond > MaxPulse {
		return fmt.Errorf("%w: break_ms must be between 1 and %d", ErrBadRequest, MaxPulse.Milliseconds())
	}
	if r.Pulse != nil {
		if r.Pulse.Line != "dtr" && r.Pulse.Line != "rts" {
			return fmt.Errorf("%w: pulse.line must be \"dtr\" or \"rts\"", ErrBadRequest)
		}
		if r.Pulse.MS < 1 || time.Duration(r.Pulse.MS)*time.Millisecond > MaxPulse {
			return fmt.Errorf("%w: pulse.ms must be between 1 and %d", ErrBadRequest, MaxPulse.Milliseconds())
		}
	}
	if r.Baud != 0 && (r.Baud < 50 || r.Baud > 20_000_000) {
		return fmt.Errorf("%w: baud %d is out of range", ErrBadRequest, r.Baud)
	}
	return nil
}

func (r ControlRequest) empty() bool {
	return r.DTR == nil && r.RTS == nil && r.BreakMS == 0 && r.Pulse == nil && r.Baud == 0
}

// Write sends p to the port as is (no newline added).
func (h *Handle) Write(ctx context.Context, p []byte) (int, error) {
	if !h.writable {
		return 0, fmt.Errorf("%s: %w", h.Stream(), ErrNotWritable)
	}
	h.ctl.Lock()
	link, err := h.connected()
	if err != nil {
		h.ctl.Unlock()
		return 0, err
	}
	type result struct {
		n   int
		err error
	}
	res := make(chan result, 1)
	// A write can block on flow control; the lock is released when it
	// returns, whether or not the caller is still waiting.
	go func() {
		defer h.ctl.Unlock()
		n, err := link.Write(p)
		res <- result{n, err}
	}()
	select {
	case r := <-res:
		return r.n, r.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (h *Handle) connected() (io.ReadWriteCloser, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.link == nil {
		reason := h.down
		if reason == "" {
			reason = "not opened yet"
		}
		return nil, fmt.Errorf("%s: %w (%s)", h.Stream(), ErrDisconnected, reason)
	}
	return h.link, nil
}

func (h *Handle) controller() (Controller, error) {
	link, err := h.connected()
	if err != nil {
		return nil, err
	}
	c, ok := link.(Controller)
	if !ok {
		reason := "this transport carries data only"
		if h.transport == TransportTCP {
			reason = "raw TCP carries data only, so line settings stay as set on the far end; use rfc2217 to control them"
		}
		return nil, &UnsupportedError{Stream: h.Stream(), Transport: h.transport, Reason: reason}
	}
	if r, ok := link.(controlRefuser); ok {
		if reason := r.ControlUnavailable(); reason != "" {
			return nil, &UnsupportedError{Stream: h.Stream(), Transport: h.transport, Reason: reason}
		}
	}
	return c, nil
}

// Control drives the lines and returns the resulting line state. A step
// that fails stops the request; the state returned is the one reached.
func (h *Handle) Control(ctx context.Context, r ControlRequest) (status.LineState, error) {
	if err := r.validate(); err != nil {
		return h.LineState(), err
	}
	if !h.writable && !r.empty() {
		return h.LineState(), fmt.Errorf("%s: %w", h.Stream(), ErrNotWritable)
	}
	h.ctl.Lock()
	defer h.ctl.Unlock()
	if r.empty() {
		h.refreshModem()
		return h.LineState(), nil
	}
	c, err := h.controller()
	if err != nil {
		return h.LineState(), err
	}
	defer h.refreshModem()
	if r.Baud != 0 {
		if err := c.SetBaud(ctx, r.Baud); err != nil {
			return h.LineState(), fmt.Errorf("%s: setting %d baud: %w", h.Stream(), r.Baud, err)
		}
		h.mu.Lock()
		h.baud = r.Baud
		h.mu.Unlock()
	}
	if r.DTR != nil {
		if err := h.setLine(ctx, c, "dtr", *r.DTR); err != nil {
			return h.LineState(), err
		}
	}
	if r.RTS != nil {
		if err := h.setLine(ctx, c, "rts", *r.RTS); err != nil {
			return h.LineState(), err
		}
	}
	if r.BreakMS > 0 {
		if err := c.Break(ctx, time.Duration(r.BreakMS)*time.Millisecond); err != nil {
			return h.LineState(), fmt.Errorf("%s: break: %w", h.Stream(), err)
		}
	}
	if r.Pulse != nil {
		ls := h.LineState()
		cur := ls.DTR
		if r.Pulse.Line == "rts" {
			cur = ls.RTS
		}
		// Ports open with both lines asserted, so that is the guess when
		// the state is unknown.
		rest := cur == nil || *cur
		if err := h.setLine(ctx, c, r.Pulse.Line, !rest); err != nil {
			return h.LineState(), err
		}
		t := time.NewTimer(time.Duration(r.Pulse.MS) * time.Millisecond)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
		}
		// Put the line back even if ctx ended, or the board stays in reset.
		if err := h.setLine(context.WithoutCancel(ctx), c, r.Pulse.Line, rest); err != nil {
			return h.LineState(), err
		}
	}
	return h.LineState(), ctx.Err()
}

func (h *Handle) setLine(ctx context.Context, c Controller, line string, on bool) error {
	var err error
	if line == "dtr" {
		err = c.SetDTR(ctx, on)
	} else {
		err = c.SetRTS(ctx, on)
	}
	if err != nil {
		return fmt.Errorf("%s: setting %s: %w", h.Stream(), line, err)
	}
	v := on
	if line == "dtr" {
		h.setLines(status.LineState{DTR: &v})
	} else {
		h.setLines(status.LineState{RTS: &v})
	}
	return nil
}

func (h *Handle) refreshModem() {
	h.mu.Lock()
	m, ok := h.link.(modemReader)
	h.mu.Unlock()
	if ok {
		h.setLines(m.Modem())
	}
}
