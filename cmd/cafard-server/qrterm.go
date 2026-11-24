package main

import (
	"strings"

	"rsc.io/qr"
)

// terminalQR draws a QR code for text in a terminal, two modules per
// character cell with the half-block characters, so a phone can scan a join
// link straight off the box's console.
//
// Light modules are drawn and dark ones left blank, which reads correctly
// on the light-on-dark terminals a console usually is; phone cameras read
// the inverted code as well. A quiet zone of two modules goes all round.
func terminalQR(text string) string {
	c, err := qr.Encode(text, qr.L)
	if err != nil {
		return ""
	}
	const quiet = 2
	n := c.Size + 2*quiet
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.Black(x, y)
	}
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		b.WriteString("  ")
		for x := 0; x < n; x++ {
			top, bottom := !dark(x, y), y+1 < n && !dark(x, y+1)
			if y+1 >= n {
				bottom = false
			}
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
