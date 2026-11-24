package client

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePort says its lines, then fails as an unplugged device does.
type fakePort struct {
	r      io.Reader
	closed bool
}

func (p *fakePort) Read(b []byte) (int, error) {
	if p.closed {
		return 0, errors.New("port closed")
	}
	n, err := p.r.Read(b)
	if errors.Is(err, io.EOF) {
		return n, errors.New("read /dev/ttyUSB0: input/output error")
	}
	return n, err
}

func (p *fakePort) Close() error { p.closed = true; return nil }

// sink is a fake cafard: it keeps every upload, by file name.
type sink struct {
	mu    sync.Mutex
	files map[string]string
	texts map[string]string
}

func newSink(t *testing.T) (*sink, *Client) {
	s := &sink{files: map[string]string{}, texts: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		s.mu.Lock()
		s.files[name] += string(b)
		s.texts[name] = r.URL.Query().Get("text")
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"address":"be98/wifi-drops","message":{"id":1}}`)
	}))
	t.Cleanup(srv.Close)
	return s, &Client{URL: srv.URL, Token: "cfd_dv_x", HTTP: srv.Client()}
}

// Two connections of the device are two logs; the port missing between
// them is waited for; -once stops after the first.
func TestSerialSessions(t *testing.T) {
	s, c := newSink(t)
	sessions := []string{"U-Boot 2024.01\nbooting\n", "Linux version 6.6\n"}
	opened := 0
	restore := openPort
	t.Cleanup(func() { openPort, retryEvery = restore, 2*time.Second })
	retryEvery = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	openPort = func(name string, baud int) (io.ReadCloser, error) {
		if name != "/dev/ttyUSB0" || baud != 921600 {
			t.Errorf("opened %s at %d", name, baud)
		}
		opened++
		switch {
		case opened == 2: // unplugged a moment
			return nil, errors.New("no such file or directory")
		case opened > 3:
			cancel()
			return nil, errors.New("no such file or directory")
		}
		return &fakePort{r: strings.NewReader(sessions[(opened-1)/2])}, nil
	}
	var out, errOut bytes.Buffer
	if code := serialCommand(ctx, c, []string{"be98/wifi-drops", "/dev/ttyUSB0", "921600", "-name", "uart.log"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var logs []string
	for name, body := range s.files {
		logs = append(logs, body)
		if !strings.HasPrefix(name, "uart-") || !strings.HasSuffix(name, ".log") {
			t.Errorf("a session's name: %q", name)
		}
		if s.texts[name] != "serial /dev/ttyUSB0 at 921600 bauds" {
			t.Errorf("its text: %q", s.texts[name])
		}
	}
	// Both sessions can start in the same minute and share a name; what
	// matters is that each was sent whole.
	all := strings.Join(logs, "")
	if !strings.Contains(all, sessions[0]) || !strings.Contains(all, sessions[1]) {
		t.Errorf("sent: %q", logs)
	}
	if out.String() != sessions[0]+sessions[1] {
		t.Errorf("shown: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "went away") || !strings.Contains(errOut.String(), "waiting for /dev/ttyUSB0") {
		t.Errorf("said: %s", errOut.String())
	}

	// -once: the first session alone.
	s2, c2 := newSink(t)
	opened = 0
	openPort = func(string, int) (io.ReadCloser, error) {
		opened++
		return &fakePort{r: strings.NewReader("only\n")}, nil
	}
	if code := serialCommand(context.Background(), c2, []string{"be98/wifi-drops", "COM3", "-once", "-quiet"}, &out, &errOut); code != 0 || opened != 1 || len(s2.files) != 1 {
		t.Errorf("-once: exit %d, opened %d, %d files", code, opened, len(s2.files))
	}
}

func TestSerialArguments(t *testing.T) {
	_, c := newSink(t)
	var out, errOut bytes.Buffer
	if code := serialCommand(context.Background(), c, []string{"be98/x", "COM3", "fast"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "bauds") {
		t.Errorf("a speed that is not one: %d %s", code, errOut.String())
	}
	if code := serialCommand(context.Background(), c, []string{"be98/x"}, &out, &errOut); code != 2 {
		t.Errorf("no port: %d", code)
	}
}

func TestStamper(t *testing.T) {
	at := time.Date(2026, 11, 24, 14, 2, 3, 123e6, time.UTC)
	s := &stamper{r: bufio.NewReader(strings.NewReader("one\ntw")), now: func() time.Time { return at }}
	got, _ := io.ReadAll(s)
	if string(got) != "14:02:03.123 one\n14:02:03.123 tw" {
		t.Errorf("%q", got)
	}
}

func TestSessionName(t *testing.T) {
	at := time.Date(2026, 11, 24, 14, 2, 0, 0, time.Local)
	if got := sessionName("console.log", at); got != "console-14h02.log" {
		t.Errorf("%q", got)
	}
	if got := sessionName("uart", at); got != "uart-14h02" {
		t.Errorf("%q", got)
	}
}
