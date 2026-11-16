package term

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// enableVTOutput lets the Windows console interpret the escape sequences
// boards print (colours, cursor moves), as a Unix terminal does. Windows
// Terminal does it already; the classic console needs the mode bit.
func enableVTOutput(w io.Writer) func() {
	f, ok := w.(*os.File)
	if !ok {
		return func() {}
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {}
	}
	if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN) != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}
