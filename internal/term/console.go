package term

import (
	"io"
	"os"

	xterm "golang.org/x/term"
)

// StdConsole is the process's terminal. When stdin is not a terminal
// (piped input), raw mode is skipped and bytes pass as they come.
type StdConsole struct {
	In  *os.File
	Out io.Writer
}

func (c StdConsole) Read(p []byte) (int, error)  { return c.In.Read(p) }
func (c StdConsole) Write(p []byte) (int, error) { return c.Out.Write(p) }

// MakeRaw implements Console.
func (c StdConsole) MakeRaw() (func() error, error) {
	fd := int(c.In.Fd())
	if !xterm.IsTerminal(fd) {
		return func() error { return nil }, nil
	}
	old, err := xterm.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	restoreOut := enableVTOutput(c.Out)
	return func() error {
		restoreOut()
		return xterm.Restore(fd, old)
	}, nil
}
