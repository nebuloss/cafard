package term

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/ingest/serial"
	"github.com/nebuloss/cafard/internal/push"
	"github.com/nebuloss/cafard/internal/testutil"
)

// fakePort is a board on a serial line: the test feeds what it prints
// and reads what it was sent.
type fakePort struct {
	out    chan []byte
	closed chan struct{}
	once   sync.Once
	mu     sync.Mutex
	got    bytes.Buffer
}

func newFakePort() *fakePort {
	return &fakePort{out: make(chan []byte, 16), closed: make(chan struct{})}
}

func (f *fakePort) Read(p []byte) (int, error) {
	select {
	case b := <-f.out:
		return copy(p, b), nil
	case <-f.closed:
		return 0, errors.New("port closed")
	}
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.got.Write(p)
}

func (f *fakePort) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *fakePort) received() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.got.String()
}

// fakeConsole is the user's terminal: keys from a pipe, output kept.
type fakeConsole struct {
	keys     *io.PipeReader
	type_    *io.PipeWriter
	mu       sync.Mutex
	out      bytes.Buffer
	raw      bool
	restored bool
}

func newFakeConsole() *fakeConsole {
	r, w := io.Pipe()
	return &fakeConsole{keys: r, type_: w}
}

func (c *fakeConsole) Read(p []byte) (int, error) { return c.keys.Read(p) }

func (c *fakeConsole) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.Write(p)
}

func (c *fakeConsole) MakeRaw() (func() error, error) {
	c.mu.Lock()
	c.raw = true
	c.mu.Unlock()
	return func() error {
		c.mu.Lock()
		c.restored = true
		c.mu.Unlock()
		return nil
	}, nil
}

func (c *fakeConsole) output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.String()
}

func (c *fakeConsole) press(s string) {
	go func() { _, _ = c.type_.Write([]byte(s)) }()
}

// logServer is POST /api/log, keeping the lines.
type logServer struct {
	mu    sync.Mutex
	lines []string
}

func (s *logServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, "not gzip", http.StatusBadRequest)
		return
	}
	sc := bufio.NewScanner(zr)
	s.mu.Lock()
	for sc.Scan() {
		var l struct{ Text string }
		_ = json.Unmarshal(sc.Bytes(), &l)
		s.lines = append(s.lines, l.Text)
	}
	s.mu.Unlock()
	_, _ = w.Write([]byte(`{"accepted":1,"last_seq":1}`))
}

func (s *logServer) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

func TestTermRelaysBothWaysPushesAndQuitsOnCtrlBracket(t *testing.T) {
	logs := &logServer{}
	srv := httptest.NewServer(logs)
	defer srv.Close()
	p, err := push.New(push.Options{Server: srv.URL, Token: "cfd_x", Stream: "bench", BatchEvery: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	port := newFakePort()
	console := newFakeConsole()
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{
			Port: serial.Port{Device: "COM5", Baud: 115200}, Console: console,
			Open: func(serial.Port) (Port, error) { return port, nil },
			Push: p.Reader,
		})
	}()
	port.out <- []byte("U-Boot 2024.04\r\nlogin: ")
	testutil.Eventually(t, "the board's output on the console", 5*time.Second, func() bool { return strings.Contains(console.output(), "login: ") })
	console.press("root\r")
	testutil.Eventually(t, "the keys on the port", 5*time.Second, func() bool { return port.received() == "root\r" })
	port.out <- []byte("\r\nPassword: ")
	testutil.Eventually(t, "the second prompt", 5*time.Second, func() bool { return strings.Contains(console.output(), "Password: ") })
	console.press("se\x03cret\x1dnot sent")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl-] did not end the session")
	}
	if got := port.received(); got != "root\rse\x03cret" {
		t.Errorf("port got %q: Ctrl-C is a byte for the board, and nothing after Ctrl-] is sent", got)
	}
	if !console.raw || !console.restored {
		t.Errorf("raw=%v restored=%v", console.raw, console.restored)
	}
	got := strings.Join(logs.got(), "|")
	if !strings.Contains(got, "U-Boot 2024.04") || !strings.Contains(got, "login:") || !strings.Contains(got, "Password:") {
		t.Errorf("pushed %q", got)
	}
}

func TestTermReopensAPortThatGoesAway(t *testing.T) {
	first, second := newFakePort(), newFakePort()
	var mu sync.Mutex
	opens := 0
	console := newFakeConsole()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Port: serial.Port{Device: "/dev/ttyUSB0"}, Console: console, MinBackoff: time.Millisecond,
			Open: func(serial.Port) (Port, error) {
				mu.Lock()
				defer mu.Unlock()
				opens++
				switch opens {
				case 1:
					return first, nil
				case 2, 3:
					return nil, errors.New("no such device")
				}
				return second, nil
			},
		})
	}()
	first.out <- []byte("before\r\n")
	testutil.Eventually(t, "output before the unplug", 5*time.Second, func() bool { return strings.Contains(console.output(), "before") })
	first.Close()
	testutil.Eventually(t, "the lost-port note", 5*time.Second, func() bool { return strings.Contains(console.output(), "went away") })
	console.press("x")
	testutil.Eventually(t, "the reopen", 5*time.Second, func() bool { return strings.Contains(console.output(), "is back") })
	second.out <- []byte("after\r\n")
	testutil.Eventually(t, "output after the replug", 5*time.Second, func() bool { return strings.Contains(console.output(), "after") })
	console.press("y")
	testutil.Eventually(t, "keys on the new port", 5*time.Second, func() bool { return second.received() == "y" })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the session")
	}
}

func TestTermFailsAtOnceOnAMissingPort(t *testing.T) {
	console := newFakeConsole()
	err := Run(context.Background(), Options{Port: serial.Port{Device: "COM9"}, Console: console,
		Open: func(serial.Port) (Port, error) { return nil, errors.New("COM9: not found") }})
	if err == nil || console.raw {
		t.Fatalf("err = %v, raw = %v: a missing port fails before touching the console", err, console.raw)
	}
}
