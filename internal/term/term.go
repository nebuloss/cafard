// Package term is `cafard term`: a serial terminal that shows a port's
// output, sends keystrokes to it and pushes everything it reads to a
// cafard server. Windows lets one program open a COM port at a time, so
// this replaces PuTTY there rather than running beside it.
package term

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/ingest/serial"
)

// Escape is Ctrl-], which ends the session, as in telnet.
const Escape = 0x1d

// Console is the user's terminal: keystrokes in, the port's output out.
type Console interface {
	io.Reader
	io.Writer
	// MakeRaw puts the terminal in raw mode (no echo, no line editing,
	// Ctrl-C as a byte) and returns what restores it.
	MakeRaw() (restore func() error, err error)
}

// Port is an open serial port.
type Port = io.ReadWriteCloser

// Options configure a session.
type Options struct {
	Port    serial.Port // Device and line settings
	Console Console
	// Open opens the port; default serial.OpenLocal. Tests use fakes.
	Open func(serial.Port) (Port, error)
	// Push, when set, receives everything read from the port, as
	// push.Pusher.Reader does: it reads until EOF, then delivers what is
	// left and returns.
	Push func(ctx context.Context, r io.Reader) error
	// PushGrace is how long the pusher may take to deliver after the
	// session ends; default 5 s. Lines it has not sent by then stay in its
	// spool when it has one.
	PushGrace  time.Duration
	Log        *slog.Logger
	MinBackoff time.Duration // reopening a lost port; default 1 s
	MaxBackoff time.Duration // default 30 s
}

func (o *Options) defaults() {
	if o.Open == nil {
		o.Open = func(p serial.Port) (Port, error) {
			l, err := serial.OpenLocal(p)
			if err != nil {
				return nil, err
			}
			return l, nil
		}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.PushGrace <= 0 {
		o.PushGrace = 5 * time.Second
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = time.Second
	}
	if o.MaxBackoff < o.MinBackoff {
		o.MaxBackoff = max(30*time.Second, o.MinBackoff)
	}
}

// session holds the port, which the reader reopens when it goes away.
type session struct {
	opts Options
	out  *lockedWriter

	mu   sync.Mutex
	port Port // nil while the port is lost
	gone bool // the writer already said the port is away
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// note prints a line of cafard's own between the port's output, on a line
// of its own so it is not taken for the device's.
func (s *session) note(format string, args ...any) {
	fmt.Fprintf(s.out, "\r\n[cafard term: "+format+"]\r\n", args...)
}

// Run opens the port, puts the console in raw mode and relays both ways
// until Ctrl-] or ctx ends. The first open must succeed, so a mistyped
// COM5 fails at once; a port that goes away later is reopened.
func Run(ctx context.Context, opts Options) error {
	opts.defaults()
	if opts.Console == nil {
		return errors.New("term needs a console")
	}
	port, err := opts.Open(opts.Port)
	if err != nil {
		return err
	}
	restore, err := opts.Console.MakeRaw()
	if err != nil {
		port.Close()
		return fmt.Errorf("raw console: %w", err)
	}
	defer func() { _ = restore() }()

	s := &session{opts: opts, out: &lockedWriter{w: opts.Console}, port: port}
	s.note("%s at %d baud. Ctrl-] quits", opts.Port.Device, baud(opts.Port))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// What the port says goes to the console and, through a pipe, to the
	// pusher.
	var (
		pushIn    *io.PipeWriter
		pushDone  = make(chan error, 1)
		pushCtx   context.Context
		stopPush  context.CancelFunc
		readerEnd = make(chan struct{})
	)
	pushCtx, stopPush = context.WithCancel(context.WithoutCancel(ctx))
	defer stopPush()
	if opts.Push != nil {
		pr, pw := io.Pipe()
		pushIn = pw
		go func() {
			err := opts.Push(pushCtx, pr)
			// A pusher that gave up must not block the reader.
			pr.CloseWithError(errors.New("push stopped"))
			if err != nil && ctx.Err() == nil {
				s.note("not pushing to the server any more: %v", err)
			}
			pushDone <- err
		}()
	} else {
		pushDone <- nil
	}
	var toPush io.Writer // a nil *PipeWriter in an interface is not nil
	if pushIn != nil {
		toPush = pushIn
	}
	go func() {
		defer close(readerEnd)
		s.readPort(ctx, toPush)
	}()

	quit := make(chan struct{})
	go func() {
		s.readKeys(ctx)
		close(quit)
	}()
	select {
	case <-quit:
	case <-ctx.Done():
	}
	cancel()
	s.closePort()
	<-readerEnd
	if pushIn != nil {
		pushIn.Close()
	}
	var pushErr error
	select {
	case pushErr = <-pushDone:
	case <-time.After(opts.PushGrace):
		stopPush()
		pushErr = <-pushDone
	}
	s.note("closed %s", opts.Port.Device)
	if pushErr != nil && !errors.Is(pushErr, context.Canceled) {
		return fmt.Errorf("push: %w", pushErr)
	}
	return nil
}

func baud(p serial.Port) int {
	if p.Baud > 0 {
		return p.Baud
	}
	return serial.DefaultBaud
}

func (s *session) current() Port {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

func (s *session) closePort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.port != nil {
		s.port.Close()
		s.port = nil
	}
}

// readPort copies the port to the console and the pusher, reopening the
// port with backoff when it goes away.
func (s *session) readPort(ctx context.Context, push io.Writer) {
	buf := make([]byte, 4096)
	backoff := s.opts.MinBackoff
	for ctx.Err() == nil {
		port := s.current()
		if port == nil {
			t := time.NewTimer(backoff)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return
			}
			p, err := s.opts.Open(s.opts.Port)
			if err != nil {
				backoff = min(2*backoff, s.opts.MaxBackoff)
				continue
			}
			s.mu.Lock()
			if ctx.Err() != nil {
				s.mu.Unlock()
				p.Close()
				return
			}
			s.port, s.gone = p, false
			s.mu.Unlock()
			backoff = s.opts.MinBackoff
			s.note("%s is back", s.opts.Port.Device)
			continue
		}
		n, err := port.Read(buf)
		if n > 0 {
			_, _ = s.out.Write(buf[:n])
			if push != nil {
				if _, perr := push.Write(buf[:n]); perr != nil {
					push = nil // the pusher said why; the terminal goes on
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			if s.port == port {
				port.Close()
				s.port = nil
			}
			s.mu.Unlock()
			s.note("%s went away (%v); reopening", s.opts.Port.Device, err)
		}
	}
}

// readKeys sends keystrokes to the port until Ctrl-] or the end of input.
func (s *session) readKeys(ctx context.Context) {
	buf := make([]byte, 1024)
	for ctx.Err() == nil {
		n, err := s.opts.Console.Read(buf)
		if n > 0 {
			data, quit := buf[:n], false
			if i := bytes.IndexByte(data, Escape); i >= 0 {
				data, quit = data[:i], true
			}
			if len(data) > 0 {
				s.write(data)
			}
			if quit {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *session) write(data []byte) {
	s.mu.Lock()
	port := s.port
	say := port == nil && !s.gone
	if port == nil {
		s.gone = true
	}
	s.mu.Unlock()
	if port == nil {
		if say {
			s.note("%s is away; keys are dropped until it is back", s.opts.Port.Device)
		}
		return
	}
	if _, err := port.Write(data); err != nil {
		s.opts.Log.Debug("write to the port failed", "err", err)
	}
}
