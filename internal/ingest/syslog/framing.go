package syslog

import (
	"time"

	"github.com/nebuloss/cafard/internal/textclean"
)

// MaxFrame bounds one octet-counted message; the rest of a longer frame
// is read and discarded.
const MaxFrame = 256 << 10

type framing int

const (
	framingUnknown framing = iota
	framingOctet
	framingNewline
)

// framer splits a TCP byte stream into syslog messages (RFC 6587). The
// first byte of the connection picks the method: a digit means octet
// counting ("LEN SP MSG"), anything else newline-delimited lines through
// a textclean.Splitter. An octet stream that stops making sense falls
// back to newlines for the rest of the connection.
type framer struct {
	mode framing
	emit func(msg []byte, at time.Time)
	sp   *textclean.Splitter

	// octet counting state
	inBody  bool
	length  []byte // digits read so far
	body    []byte
	need    int // body bytes still to read
	discard int // bytes beyond MaxFrame still to skip
	bodyAt  time.Time
}

func newFramer(emit func(msg []byte, at time.Time)) *framer {
	f := &framer{emit: emit}
	f.sp = textclean.NewSplitter(textclean.DefaultFlushAfter, textclean.MaxLineBytes, func(line string, at time.Time) {
		if line != "" {
			f.emit([]byte(line), at)
		}
	})
	return f
}

// Write feeds bytes received at at.
func (f *framer) Write(p []byte, at time.Time) {
	for len(p) > 0 {
		switch f.mode {
		case framingUnknown:
			c := p[0]
			if c == '\n' || c == '\r' || c == ' ' {
				p = p[1:]
				continue
			}
			if c >= '1' && c <= '9' {
				f.mode = framingOctet
			} else {
				f.mode = framingNewline
			}
		case framingNewline:
			f.sp.Write(p, at)
			return
		case framingOctet:
			p = f.octet(p, at)
		}
	}
}

// octet consumes what it can of p in octet-counting mode.
func (f *framer) octet(p []byte, at time.Time) []byte {
	if f.inBody {
		if f.discard > 0 && f.need == 0 {
			n := min(f.discard, len(p))
			f.discard -= n
			p = p[n:]
			if f.discard == 0 {
				f.inBody = false
			}
			return p
		}
		n := min(f.need, len(p))
		f.body = append(f.body, p[:n]...)
		f.need -= n
		p = p[n:]
		if f.need == 0 {
			f.emit(f.body, f.bodyAt)
			f.body = nil
			if f.discard == 0 {
				f.inBody = false
			}
		}
		return p
	}
	for i, c := range p {
		switch {
		case c >= '0' && c <= '9' && len(f.length) < 9:
			if len(f.length) == 0 && c == '0' {
				return f.fallback(p[i:], at)
			}
			f.length = append(f.length, c)
		case c == ' ' && len(f.length) > 0:
			n := 0
			for _, d := range f.length {
				n = n*10 + int(d-'0')
			}
			f.length = f.length[:0]
			f.inBody = true
			f.need = min(n, MaxFrame)
			f.discard = n - f.need
			f.bodyAt = at
			f.body = make([]byte, 0, min(f.need, 4096))
			return p[i+1:]
		case (c == '\n' || c == '\r') && len(f.length) == 0:
			// Some senders end each frame with a newline as well.
		default:
			return f.fallback(p[i:], at)
		}
	}
	return nil
}

// fallback switches to newline framing, replaying the digits it held.
func (f *framer) fallback(p []byte, at time.Time) []byte {
	f.mode = framingNewline
	if len(f.length) > 0 {
		f.sp.Write(f.length, at)
		f.length = nil
	}
	return p
}

// Tick flushes a partial newline-framed line after a silence.
func (f *framer) Tick(now time.Time) { f.sp.Tick(now) }

// Close flushes what is left: a partial line, or a truncated frame.
func (f *framer) Close() {
	f.sp.Close()
	if f.mode == framingOctet && f.inBody && len(f.body) > 0 {
		f.emit(f.body, f.bodyAt)
		f.body = nil
	}
}
