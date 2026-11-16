// Package textclean normalises raw producer bytes into lines (Splitter),
// cleans text for agents (StripANSI) and renders entries as the compact
// agent-facing text (Format).
package textclean

import (
	"time"
	"unicode/utf8"
)

// DefaultFlushAfter is how long a partial line may wait before Tick
// flushes it, so a prompt such as "login: " is not stuck in the buffer.
const DefaultFlushAfter = 500 * time.Millisecond

// MaxLineBytes is the default maximum line length before splitting.
const MaxLineBytes = 16 << 10

// ContinuationMarker is appended to every piece of a split long line
// except the last.
const ContinuationMarker = " [continued]"

var replacement = []byte(string(utf8.RuneError))

// Splitter turns a byte stream into lines. It decodes UTF-8 (invalid
// bytes become U+FFFD), ends lines on \n, \r\n and a lone \r, and splits
// lines longer than maxLine bytes. ANSI codes are kept raw.
//
// A Splitter is not safe for concurrent use.
type Splitter struct {
	flushAfter time.Duration
	maxLine    int
	emit       func(line string, at time.Time)

	buf     []byte    // current line, valid UTF-8
	started bool      // a line is in progress (bytes, a pending rune or a pending \r)
	lineAt  time.Time // time of the Write that started the current line

	tail   []byte    // incomplete UTF-8 rune at the end of the last Write
	tailAt time.Time // time of the Write that delivered tail

	pendingCR bool // the last byte seen was \r: the line ends, but a \n may follow
	skipLF    bool // the line ended on \r (flushed by Tick): drop a leading \n
}

// NewSplitter returns a Splitter that calls emit for every line.
// maxLine <= 0 means MaxLineBytes.
func NewSplitter(flushAfter time.Duration, maxLine int, emit func(line string, at time.Time)) *Splitter {
	if maxLine <= 0 {
		maxLine = MaxLineBytes
	}
	return &Splitter{flushAfter: flushAfter, maxLine: maxLine, emit: emit}
}

// Write feeds p, received at time at.
func (s *Splitter) Write(p []byte, at time.Time) {
	if len(p) == 0 {
		return
	}
	data := p
	if len(s.tail) > 0 {
		data = append(s.tail, p...)
		s.tail = nil
	}
	i := 0
	if s.pendingCR {
		s.pendingCR = false
		if data[0] == '\n' {
			i = 1
		}
		s.endLine()
	} else if s.skipLF {
		if data[0] == '\n' {
			i = 1
		}
	}
	s.skipLF = false

	for i < len(data) {
		b := data[i]
		switch {
		case b == '\n':
			s.endLine()
			i++
		case b == '\r':
			if i+1 == len(data) {
				// Wait for the next byte to tell \r\n from a lone \r.
				s.start(at)
				s.pendingCR = true
				return
			}
			i++
			if data[i] == '\n' {
				i++
			}
			s.endLine()
		case b < utf8.RuneSelf:
			s.add(data[i:i+1], at)
			i++
		default:
			if !utf8.FullRune(data[i:]) {
				s.start(at)
				s.tail = append([]byte(nil), data[i:]...)
				s.tailAt = at
				return
			}
			r, size := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError && size == 1 {
				s.add(replacement, at)
			} else {
				s.add(data[i:i+size], at)
			}
			i += size
		}
	}
}

// Tick flushes the partial line if it started at least flushAfter ago.
// An incomplete trailing rune is kept for the next Write.
func (s *Splitter) Tick(now time.Time) {
	if !s.started || now.Sub(s.lineAt) < s.flushAfter {
		return
	}
	switch {
	case s.pendingCR:
		s.pendingCR = false
		s.skipLF = true
		s.endLine()
	case len(s.buf) > 0:
		s.endLine()
	default:
		return
	}
	if len(s.tail) > 0 {
		s.start(s.tailAt)
	}
}

// Close flushes whatever partial line remains. An incomplete trailing
// rune becomes one U+FFFD per byte, as for any other invalid byte.
func (s *Splitter) Close() {
	for range s.tail {
		s.add(replacement, s.tailAt)
	}
	s.tail = nil
	if s.started {
		s.endLine()
	}
	s.pendingCR = false
	s.skipLF = false
}

func (s *Splitter) start(at time.Time) {
	if !s.started {
		s.started = true
		s.lineAt = at
	}
}

func (s *Splitter) add(b []byte, at time.Time) {
	s.start(at)
	s.buf = append(s.buf, b...)
	for len(s.buf) > s.maxLine {
		cut := s.maxLine
		for cut > 0 && !utf8.RuneStart(s.buf[cut]) {
			cut--
		}
		if cut == 0 {
			// maxLine is smaller than the first rune: emit that rune alone,
			// once something follows it (a continued piece is never last).
			_, cut = utf8.DecodeRune(s.buf)
			if cut == len(s.buf) {
				break
			}
		}
		s.emit(string(s.buf[:cut])+ContinuationMarker, s.lineAt)
		n := copy(s.buf, s.buf[cut:])
		s.buf = s.buf[:n]
		s.lineAt = at
	}
}

func (s *Splitter) endLine() {
	s.emit(string(s.buf), s.lineAt)
	s.buf = s.buf[:0]
	s.started = false
}
