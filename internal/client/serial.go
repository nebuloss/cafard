package client

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"
)

// mouchard serial: a serial console as a live log, on Linux, macOS and
// Windows alike (go.bug.st/serial, pure Go). It is what `stty` and
// `cat /dev/ttyUSB0 | curl -T -` did, without their failure: a board
// unplugged, or an adapter that resets with it, ended the pipe for good.
// Here a session is one connection of the device: when the port goes away
// the log in progress ends, and when the port comes back a new one starts,
// named for the time it started (console-14h02.log), so each boot reads on
// its own.

// openPort opens a serial port; tests put a fake in its place.
var openPort = func(name string, baud int) (io.ReadCloser, error) {
	return serial.Open(name, &serial.Mode{BaudRate: baud})
}

// retryEvery is how often a port that is not there is tried again.
var retryEvery = 2 * time.Second

func serialCommand(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serial", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "console.log", "the live file's name; each session's has its start time in it")
	stamp := fs.Bool("time", false, "begin each line with the time it arrived")
	quiet := fs.Bool("quiet", false, "do not also show the console here")
	once := fs.Bool("once", false, "stop when the port goes away, rather than wait for it to come back")
	pos, err := flags(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) < 2 || len(pos) > 3 {
		fmt.Fprint(stderr, Usage)
		return 2
	}
	to, err := ParseAddress(pos[0])
	if err != nil {
		fmt.Fprintln(stderr, "mouchard:", err)
		return 2
	}
	port, baud := pos[1], 115200
	if len(pos) == 3 {
		if baud, err = strconv.Atoi(pos[2]); err != nil || baud <= 0 {
			fmt.Fprintf(stderr, "mouchard: %q is not a speed: give it in bauds, e.g. 115200\n", pos[2])
			return 2
		}
	}
	// Ctrl-C ends the session in progress cleanly: its log is closed and
	// stored, then mouchard exits.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	waiting := false
	for ctx.Err() == nil {
		p, err := openPort(port, baud)
		if err != nil {
			if !waiting {
				fmt.Fprintf(stderr, "mouchard: waiting for %s (%v)\n", port, err)
				waiting = true
			}
			select {
			case <-ctx.Done():
			case <-time.After(retryEvery):
			}
			continue
		}
		waiting = false
		file := sessionName(*name, time.Now())
		fmt.Fprintf(stderr, "mouchard: %s open at %d bauds, sending %s to %s\n", port, baud, file, to)
		var show io.Writer
		if !*quiet {
			show = stdout
		}
		err = session(ctx, c, to, file, fmt.Sprintf("serial %s at %d bauds", port, baud), p, *stamp, show, stderr)
		if err != nil {
			fmt.Fprintln(stderr, "mouchard:", err)
		}
		if ctx.Err() != nil {
			break
		}
		fmt.Fprintf(stderr, "mouchard: %s went away\n", port)
		if *once {
			break
		}
	}
	return 0
}

// session sends what the port says until it goes away or ctx ends.
func session(ctx context.Context, c *Client, to Address, file, text string, p io.ReadCloser, stamp bool, show, stderr io.Writer) error {
	// Closing the port is what ends a read waiting on it, on Ctrl-C.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			p.Close()
		case <-done:
		}
	}()
	defer p.Close()

	var r io.Reader = p
	if stamp {
		r = &stamper{r: bufio.NewReader(p), now: time.Now}
	}
	if show != nil {
		r = io.TeeReader(r, show)
	}
	// A port that went away is the end of the session, not an error: the
	// reader stops, and what it read is the log.
	r = &endOnError{r: r}
	// The upload outlives ctx: Ctrl-C closes the port, and the log is then
	// stored to its last byte rather than cut off.
	_, err := stream(context.WithoutCancel(ctx), c, to, file, text, r, stderr)
	return err
}

// sessionName puts the session's start in a log's name: console.log
// becomes console-14h02.log.
func sessionName(name string, at time.Time) string {
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + "-" + at.Format("15h04") + ext
}

// endOnError reads until its reader fails, then reports the end: an
// unplugged device is a read error on every system, and the end of a log.
type endOnError struct{ r io.Reader }

func (e *endOnError) Read(b []byte) (int, error) {
	n, err := e.r.Read(b)
	if err != nil && !errors.Is(err, io.EOF) {
		err = io.EOF
	}
	if n == 0 && err == nil {
		// go.bug.st/serial answers 0, nil when a read times out; none is
		// set, so this is a port that closed under it.
		return 0, io.EOF
	}
	return n, err
}

// stamper puts the time a line arrived in front of it: "14:02:03.123 ".
type stamper struct {
	r   *bufio.Reader
	now func() time.Time
	buf []byte
	mid bool // inside a line already stamped
}

func (s *stamper) Read(b []byte) (int, error) {
	for len(s.buf) == 0 {
		chunk, err := s.r.ReadSlice('\n')
		if len(chunk) > 0 {
			if !s.mid {
				s.buf = append(s.buf, s.now().Format("15:04:05.000 ")...)
				s.mid = true
			}
			s.buf = append(s.buf, chunk...)
			if chunk[len(chunk)-1] == '\n' {
				s.mid = false
			}
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			if len(s.buf) > 0 {
				break
			}
			return 0, err
		}
	}
	n := copy(b, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}
