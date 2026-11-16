package serial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
)

// ClaimChecker is the part of messaging the claim gate needs. It is
// asserted on deps.Messaging, so serial does not import messaging.
type ClaimChecker interface {
	Holds(ctx context.Context, p auth.Principal, resource string) (bool, error)
}

// Limits of the tools.
const (
	MaxWrite        = 16 << 10 // bytes of one serial_write, newline included
	maxMarkerData   = 300      // bytes of escaped data shown in a write marker
	retryBusyAfterS = 5        // the reconnect backoff starts at 1 s and stops at 30 s
)

type writeIn struct {
	Stream  string  `json:"stream" jsonschema:"the port's stream, e.g. serial:dut1"`
	Data    string  `json:"data" jsonschema:"text to send, e.g. reboot"`
	Newline *string `json:"newline,omitempty" jsonschema:"appended to data; default \"\\r\" (what a terminal's Enter sends); \"\" for none"`
}

type writeOut struct {
	BytesWritten int   `json:"bytes_written" jsonschema:"bytes sent to the port, newline included"`
	MarkerSeq    int64 `json:"marker_seq" jsonschema:"the seq of the marker recording the write; read since_seq=marker_seq+1 for the reply"`
}

type pulseIn struct {
	Line string `json:"line" jsonschema:"dtr or rts"`
	MS   int    `json:"ms" jsonschema:"how long the line is flipped, 1 to 10000 ms"`
}

type controlIn struct {
	Stream  string   `json:"stream" jsonschema:"the port's stream, e.g. serial:dut1"`
	DTR     *bool    `json:"dtr,omitempty" jsonschema:"set DTR on (true) or off (false)"`
	RTS     *bool    `json:"rts,omitempty" jsonschema:"set RTS on (true) or off (false)"`
	BreakMS int      `json:"break_ms,omitempty" jsonschema:"send a break of this many ms, at most 10000"`
	Pulse   *pulseIn `json:"pulse,omitempty" jsonschema:"flip a line and put it back, e.g. {line: dtr, ms: 100} to reset a board"`
	Baud    int      `json:"baud,omitempty" jsonschema:"new baud rate, kept across reconnects until cafard restarts"`
}

type controlOut struct {
	Stream    string        `json:"stream"`
	Lines     dto.LineState `json:"lines" jsonschema:"the line state after the request; a missing line is unknown"`
	Baud      int           `json:"baud,omitempty"`
	MarkerSeq int64         `json:"marker_seq,omitempty" jsonschema:"the seq of the marker recording the action; absent when nothing was changed"`
}

// tools holds what the serial_write and serial_control handlers need.
type tools struct {
	m      *Manager
	claims ClaimChecker          // nil without messaging: every write is refused
	owners mcpserver.ClaimSource // optional, to name the holder
}

func registerTools(s *mcpserver.Server, m *Manager) {
	t := newTools(m)
	mcpserver.AddTool(s, auth.ScopeSerialWrite, mcp.Tool{Name: "serial_write",
		Description: "Type into a serial console: sends data plus newline (default \\r) to a port. Needs the port writable in the config and your claim on its stream (claim first, release after). Returns the bytes written and the seq of the marker that records it; the board's answer follows in the stream."},
		t.write)
	mcpserver.AddTool(s, auth.ScopeSerialWrite, mcp.Tool{Name: "serial_control",
		Description: "Drive a serial port's lines: dtr, rts, break_ms, pulse {line, ms} (pulse dtr for 100 ms resets most boards wired for it), baud. Steps run in that order: baud, dtr, rts, break, pulse. Works on local and rfc2217 ports, not raw tcp. Same gates as serial_write. Returns the resulting line state."},
		t.control)
}

func newTools(m *Manager) *tools {
	t := &tools{m: m}
	t.claims, _ = m.d.Messaging.(ClaimChecker)
	t.owners, _ = m.d.Messaging.(mcpserver.ClaimSource)
	return t
}

// gate runs the three checks (the scope is the tool's own) and returns the
// port.
func (t *tools) gate(ctx context.Context, p auth.Principal, stream string) (*Handle, error) {
	h, err := t.m.Lookup(stream)
	if err != nil {
		names := make([]string, 0, len(t.m.ports))
		for _, h := range t.m.ports {
			names = append(names, h.Stream())
		}
		return nil, dto.Errorf(dto.CodeNotFound, "There is no serial port with stream %q; the configured ones are %s.", stream, strings.Join(names, ", "))
	}
	if !h.Writable() {
		return nil, dto.Errorf(dto.CodeForbidden, "%s is read-only: the operator must set writable: true on port %s in cafard.yaml before agents may drive it.",
			stream, h.port.Name)
	}
	if t.claims == nil {
		return nil, dto.Errorf(dto.CodeForbidden, "Writing to %s needs a claim, and this server has no claims.", stream)
	}
	ok, err := t.claims.Holds(ctx, p, stream)
	if err != nil {
		return nil, err
	}
	if ok {
		return h, nil
	}
	if t.owners != nil {
		claims, err := t.owners.ActiveClaims(ctx)
		if err != nil {
			return nil, err
		}
		if c, held := claims[stream]; held {
			return nil, dto.Errorf(dto.CodeConflict, "%s is claimed by %s until %s; only the claim holder may write to it or drive its lines.",
				stream, c.Principal, c.ExpiresAt.UTC().Format("15:04:05 UTC"))
		}
	}
	return nil, dto.Errorf(dto.CodeForbidden, "Claim %s first (claim with resource %q), then write; release it when you are done.", stream, stream)
}

// apiError turns an error of Write or Control into the sentence an agent
// reads. Errors it does not know are returned as is.
func apiError(err error) error {
	var ue *UnsupportedError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ue):
		return dto.Errorf(dto.CodeBadRequest, "%s cannot drive DTR, RTS, break or the baud rate: %s.", ue.Stream, ue.Reason)
	case errors.Is(err, ErrDisconnected):
		return dto.Errorf(dto.CodeBusy, "%s; cafard reopens the port on its own, try again in %d s.", sentence(err), retryBusyAfterS).RetryAfter(retryBusyAfterS)
	case errors.Is(err, ErrBadRequest):
		return dto.Errorf(dto.CodeBadRequest, "%s.", sentence(err))
	case errors.Is(err, ErrNotWritable):
		return dto.Errorf(dto.CodeForbidden, "%s.", sentence(err))
	case errors.Is(err, ErrUnknownStream):
		return dto.Errorf(dto.CodeNotFound, "%s.", sentence(err))
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	}
	return dto.Errorf(dto.CodeBusy, "The port did not do it: %s.", sentence(err)).RetryAfter(retryBusyAfterS)
}

func sentence(err error) string {
	s := strings.TrimSuffix(err.Error(), ".")
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return strings.ToUpper(string(r)) + s[n:]
}

func (t *tools) write(ctx context.Context, c *mcpserver.Call, in writeIn) (string, writeOut, error) {
	h, err := t.gate(ctx, c.Principal, in.Stream)
	if err != nil {
		return "", writeOut{}, err
	}
	nl := "\r"
	if in.Newline != nil {
		nl = *in.Newline
	}
	data := in.Data + nl
	if data == "" {
		return "", writeOut{}, dto.Errorf(dto.CodeBadRequest, "There is nothing to write: data and newline are both empty.")
	}
	if len(data) > MaxWrite {
		return "", writeOut{}, dto.Errorf(dto.CodeTooLarge, "One serial_write sends at most %d bytes; split the data.", MaxWrite)
	}
	if _, err := h.connected(); err != nil {
		return "", writeOut{}, apiError(err)
	}
	// The marker goes first, so the board's echo and answer come after it
	// in seq order.
	seq, err := t.marker(ctx, h, c.Principal, "write", c.Principal.Name+"> "+Escape([]byte(data), maxMarkerData), store.DefaultSeverity)
	if err != nil {
		return "", writeOut{}, err
	}
	n, err := h.Write(ctx, []byte(data))
	if err != nil {
		t.failed(h, c.Principal, "write", err)
		return "", writeOut{}, apiError(err)
	}
	return fmt.Sprintf("Wrote %d bytes to %s (marker %d). Pass since_seq=%d to read or wait_for what the board answers.", n, in.Stream, seq, seq+1),
		writeOut{BytesWritten: n, MarkerSeq: seq}, nil
}

func (t *tools) control(ctx context.Context, c *mcpserver.Call, in controlIn) (string, controlOut, error) {
	h, err := t.gate(ctx, c.Principal, in.Stream)
	if err != nil {
		return "", controlOut{}, err
	}
	req := ControlRequest{DTR: in.DTR, RTS: in.RTS, BreakMS: in.BreakMS, Baud: in.Baud}
	if in.Pulse != nil {
		req.Pulse = &Pulse{Line: strings.ToLower(in.Pulse.Line), MS: in.Pulse.MS}
	}
	if err := req.validate(); err != nil {
		return "", controlOut{}, apiError(err)
	}
	var seq int64
	if !req.empty() {
		// Refuse before the marker, so a refused request leaves no trace
		// of an action that never happened.
		if _, err := h.controller(); err != nil {
			return "", controlOut{}, apiError(err)
		}
		seq, err = t.marker(ctx, h, c.Principal, "control", "serial: "+c.Principal.Name+" "+describe(req), store.DefaultSeverity)
		if err != nil {
			return "", controlOut{}, err
		}
	}
	ls, err := h.Control(ctx, req)
	if err != nil {
		if !req.empty() {
			t.failed(h, c.Principal, "control", err)
		}
		return "", controlOut{}, apiError(err)
	}
	out := controlOut{Stream: in.Stream, Lines: ls, Baud: h.current().Baud, MarkerSeq: seq}
	text := fmt.Sprintf("%s lines: %s.", in.Stream, lineText(ls))
	if !req.empty() {
		text = fmt.Sprintf("Done on %s: %s (marker %d). Lines now: %s. Pass since_seq=%d to read what the board printed.",
			in.Stream, describe(req), seq, lineText(ls), seq+1)
	}
	return text, out, nil
}

// marker records an action of p in the port's stream, through the writer.
func (t *tools) marker(ctx context.Context, h *Handle, p auth.Principal, event, text string, severity int) (int64, error) {
	meta, _ := json.Marshal(map[string]string{"serial": event})
	return t.m.d.Writer.Commit(ctx, store.Entry{Stream: h.Stream(), Kind: store.KindMarker, Severity: severity,
		Text: text, Sender: p.Name, Meta: meta})
}

// failed says in the stream that the action its marker announced did not
// (fully) happen.
func (t *tools) failed(h *Handle, p auth.Principal, event string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, merr := t.marker(ctx, h, p, event+"_failed", fmt.Sprintf("serial: %s by %s failed: %v", event, p.Name, err), 4); merr != nil {
		h.log.Warn("marker lost", "err", merr)
	}
}

// describe is a control request in words, in the order its steps run.
func describe(r ControlRequest) string {
	onOff := map[bool]string{true: "on", false: "off"}
	var parts []string
	if r.Baud != 0 {
		parts = append(parts, fmt.Sprintf("set %d baud", r.Baud))
	}
	if r.DTR != nil {
		parts = append(parts, "set DTR "+onOff[*r.DTR])
	}
	if r.RTS != nil {
		parts = append(parts, "set RTS "+onOff[*r.RTS])
	}
	if r.BreakMS > 0 {
		parts = append(parts, fmt.Sprintf("sent a %d ms break", r.BreakMS))
	}
	if r.Pulse != nil {
		parts = append(parts, fmt.Sprintf("pulsed %s for %d ms", strings.ToUpper(r.Pulse.Line), r.Pulse.MS))
	}
	return strings.Join(parts, ", ")
}

func lineText(ls status.LineState) string {
	one := func(name string, b *bool) string {
		switch {
		case b == nil:
			return name + " unknown"
		case *b:
			return name + " on"
		}
		return name + " off"
	}
	return strings.Join([]string{one("DTR", ls.DTR), one("RTS", ls.RTS), one("CTS", ls.CTS), one("DCD", ls.DCD)}, ", ")
}

// Escape shows bytes sent to a console as readable text: control
// characters and invalid UTF-8 become escapes, and the result is cut at
// about limit bytes with the total size.
func Escape(b []byte, limit int) string {
	var sb strings.Builder
	for i := 0; i < len(b); {
		if sb.Len() >= limit {
			fmt.Fprintf(&sb, "... (%d bytes in all)", len(b))
			break
		}
		r, n := utf8.DecodeRune(b[i:])
		switch {
		case r == utf8.RuneError && n <= 1:
			fmt.Fprintf(&sb, `\x%02x`, b[i])
		case r == '\\':
			sb.WriteString(`\\`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\t':
			sb.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&sb, `\x%02x`, r)
		case r >= 0x80 && r < 0xa0, slices.Contains([]rune{'\u2028', '\u2029', '\u200e', '\u200f', '\u202e', '\ufeff'}, r):
			fmt.Fprintf(&sb, `\u%04x`, r)
		default:
			sb.WriteRune(r)
		}
		i += n
	}
	return sb.String()
}
