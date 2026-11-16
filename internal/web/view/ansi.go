package view

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nebuloss/cafard/internal/textclean"
)

// Segment is a run of text with the ANSI classes that apply to it. Class
// names come from a fixed list; nothing of the input reaches them but
// small numbers checked against that list.
type Segment struct {
	Class string `json:"class,omitempty"`
	Text  string `json:"text"`
}

type sgr struct {
	fg, bg                       int // -1 default, 0..15
	bold, dim, italic, underline bool
}

func (s sgr) class() string {
	var parts []string
	if s.fg >= 0 {
		parts = append(parts, "ansi-fg-"+strconv.Itoa(s.fg))
	}
	if s.bg >= 0 {
		parts = append(parts, "ansi-bg-"+strconv.Itoa(s.bg))
	}
	if s.bold {
		parts = append(parts, "ansi-bold")
	}
	if s.dim {
		parts = append(parts, "ansi-dim")
	}
	if s.italic {
		parts = append(parts, "ansi-italic")
	}
	if s.underline {
		parts = append(parts, "ansi-underline")
	}
	return strings.Join(parts, " ")
}

func (s *sgr) apply(params []int) {
	if len(params) == 0 {
		params = []int{0}
	}
	for i := 0; i < len(params); i++ {
		p := params[i]
		switch {
		case p == 0:
			*s = sgr{fg: -1, bg: -1}
		case p == 1:
			s.bold = true
		case p == 2:
			s.dim = true
		case p == 3:
			s.italic = true
		case p == 4:
			s.underline = true
		case p == 22:
			s.bold, s.dim = false, false
		case p == 23:
			s.italic = false
		case p == 24:
			s.underline = false
		case p >= 30 && p <= 37:
			s.fg = p - 30
		case p == 39:
			s.fg = -1
		case p >= 40 && p <= 47:
			s.bg = p - 40
		case p == 49:
			s.bg = -1
		case p >= 90 && p <= 97:
			s.fg = p - 90 + 8
		case p >= 100 && p <= 107:
			s.bg = p - 100 + 8
		case p == 38 || p == 48:
			// 38;5;n keeps n when it is one of the 16 palette colours;
			// 256-colour and truecolour values are dropped, never passed on.
			if i+2 < len(params) && params[i+1] == 5 {
				if n := params[i+2]; n < 16 {
					if p == 38 {
						s.fg = n
					} else {
						s.bg = n
					}
				}
				i += 2
			} else if i+1 < len(params) && params[i+1] == 2 {
				i += 4
			}
		}
	}
}

// ANSI parses SGR colour sequences into segments and drops every other
// escape sequence and control character but tab and newline. It is the
// same algorithm as src/render/AnsiParser.ts; both are checked against
// internal/web/testdata/ansi/vectors.json.
func ANSI(text string) []Segment {
	var (
		out   []Segment
		cur   = sgr{fg: -1, bg: -1}
		class string
		buf   strings.Builder
	)
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Class == class {
			out[n-1].Text += buf.String()
		} else {
			out = append(out, Segment{Class: class, Text: buf.String()})
		}
		buf.Reset()
	}
	for i := 0; i < len(text); {
		c := text[i]
		if c == 0x1b {
			flush()
			i = escape(text, i, &cur)
			class = cur.class()
			continue
		}
		if c < 0x20 && c != '\t' && c != '\n' || c == 0x7f {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			buf.WriteRune(utf8.RuneError)
		} else if r >= 0x80 && r < 0xa0 {
			// C1 controls: dropped like C0.
		} else {
			buf.WriteString(text[i : i+size])
		}
		i += size
	}
	flush()
	return out
}

// escape consumes the sequence at text[i] (an ESC) and returns the index
// after it, applying it to cur when it is SGR.
func escape(text string, i int, cur *sgr) int {
	if i+1 >= len(text) {
		return i + 1
	}
	switch text[i+1] {
	case '[':
		j := i + 2
		for j < len(text) && (text[j] < 0x40 || text[j] > 0x7e) {
			j++
		}
		if j >= len(text) {
			return j
		}
		if text[j] == 'm' {
			cur.apply(parseParams(text[i+2 : j]))
		}
		return j + 1
	case ']', 'P', '_', '^', 'X':
		// String sequences end with BEL or ST (ESC \).
		for j := i + 2; j < len(text); j++ {
			if text[j] == 0x07 {
				return j + 1
			}
			if text[j] == 0x1b && j+1 < len(text) && text[j+1] == '\\' {
				return j + 2
			}
		}
		return len(text)
	case '(', ')', '*', '+':
		// Character set designation: ESC ( B.
		return min(i+3, len(text))
	}
	// A two-character sequence: skip the whole character after ESC.
	_, size := utf8.DecodeRuneInString(text[i+1:])
	return i + 1 + size
}

func parseParams(s string) []int {
	if s == "" {
		return nil
	}
	var out []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ':' }) {
		n, err := strconv.Atoi(f)
		if err != nil || n > 255 {
			n = -1
		}
		out = append(out, n)
	}
	return out
}

// StripText is text without escape sequences or control characters, for
// places that show no colour (markers, previews).
func StripText(s string) string { return textclean.StripANSI(s) }
