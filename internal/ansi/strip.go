// Package ansi removes terminal escape sequences and control characters.
// It imports nothing of cafard, so the store can use it for the FTS index.
package ansi

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Strip removes ANSI escape sequences (CSI, OSC, DCS/SOS/PM/APC
// strings, other ESC sequences, and their 8-bit C1 forms) and every
// control character except \t. Invalid UTF-8 becomes U+FFFD.
func Strip(s string) string {
	if clean(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b:
			i = skipEscape(s, i+1)
		case r == 0x9b: // 8-bit CSI
			i = skipCSI(s, i+size)
		case r == 0x9d || r == 0x90 || r == 0x98 || r == 0x9e || r == 0x9f: // 8-bit OSC, DCS, SOS, PM, APC
			i = skipString(s, i+size)
		case r == '\t':
			b.WriteByte('\t')
			i += size
		case unicode.IsControl(r):
			i += size
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
			i++
		default:
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

// clean reports whether s needs no change, the common case.
func clean(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			return false
		}
		if (c < 0x20 && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}

// skipEscape skips the sequence after an ESC at s[i-1] and returns the
// index after it.
func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	c := s[i]
	switch {
	case c == '[':
		return skipCSI(s, i+1)
	case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
		return skipString(s, i+1)
	case c >= 0x20 && c <= 0x2f:
		// Intermediate bytes, then one final byte (e.g. ESC ( B).
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
			i++
		}
		return i
	case c >= 0x30 && c <= 0x7e:
		return i + 1
	}
	// Not a sequence: drop the ESC alone.
	return i
}

// skipCSI skips parameter and intermediate bytes and the final byte.
func skipCSI(s string, i int) int {
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
		i++
	}
	return i
}

// skipString skips an OSC-like string up to BEL, ESC \ or an 8-bit ST,
// or to the end of s when unterminated.
func skipString(s string, i int) int {
	for i < len(s) {
		switch {
		case s[i] == 0x07:
			return i + 1
		case s[i] == 0x1b:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			// An ESC that is not ST aborts the string.
			return i
		case strings.HasPrefix(s[i:], "\u009c"):
			return i + len("\u009c")
		}
		i++
	}
	return i
}
