package textclean

import "github.com/nebuloss/cafard/internal/ansi"

// StripANSI removes ANSI escape sequences (CSI, OSC, DCS/SOS/PM/APC
// strings, other ESC sequences, and their 8-bit C1 forms) and every
// control character except \t. Invalid UTF-8 becomes U+FFFD.
func StripANSI(s string) string { return ansi.Strip(s) }
