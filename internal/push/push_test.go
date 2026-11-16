package push

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeServer struct {
	mu      sync.Mutex
	lines   []string
	fail    int // answer 503 this many times first
	batches int
	auth    string
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = r.Header.Get("Authorization")
	if f.fail > 0 {
		f.fail--
		w.Header().Set("Retry-After", "1")
		http.Error(w, `{"error":{"code":"busy","message":"busy"}}`, http.StatusServiceUnavailable)
		return
	}
	if r.URL.Query().Get("stream") != "laptop" || r.Header.Get("Content-Type") != "application/x-ndjson" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, "not gzip", http.StatusBadRequest)
		return
	}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		var l wireLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil || l.TS == "" {
			http.Error(w, "bad line", http.StatusBadRequest)
			return
		}
		f.lines = append(f.lines, l.Text)
	}
	f.batches++
	w.Write([]byte(`{"accepted":1,"last_seq":1}`))
}

func TestPushBatchesAndRetries(t *testing.T) {
	f := &fakeServer{fail: 1}
	srv := httptest.NewServer(f)
	defer srv.Close()
	p, err := New(Options{Server: srv.URL + "/", Token: "cfd_x", Stream: "laptop", BatchMax: 10})
	if err != nil {
		t.Fatal(err)
	}
	var in strings.Builder
	for i := range 25 {
		in.WriteString(strings.Repeat("x", i) + "\n")
	}
	start := time.Now()
	if err := p.Reader(context.Background(), strings.NewReader(in.String()+"no newline")); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.lines) != 26 || f.lines[25] != "no newline" || f.lines[3] != "xxx" {
		t.Fatalf("server got %d lines: %q", len(f.lines), f.lines)
	}
	if f.batches < 3 {
		t.Errorf("%d batches, want at least 3 of at most 10 lines", f.batches)
	}
	if f.auth != "Bearer cfd_x" {
		t.Errorf("auth = %q", f.auth)
	}
	if time.Since(start) < time.Second {
		t.Error("the 503 was not retried after Retry-After")
	}
	if p.Sent() != 26 {
		t.Errorf("sent = %d", p.Sent())
	}
}

func TestPushFlushesAPartialLineAfterSilence(t *testing.T) {
	f := &fakeServer{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	p, _ := New(Options{Server: srv.URL, Token: "cfd_x", Stream: "laptop"})
	r, w := ioPipe()
	done := make(chan error, 1)
	go func() { done <- p.Reader(context.Background(), r) }()
	w.Write([]byte("login: "))
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		n := len(f.lines)
		f.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prompt was never sent")
		}
		time.Sleep(20 * time.Millisecond)
	}
	w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f.lines[0] != "login: " {
		t.Errorf("line = %q", f.lines[0])
	}
}

func TestOptionsAreChecked(t *testing.T) {
	for _, o := range []Options{
		{Token: "t", Stream: "s"},
		{Server: "http://x", Stream: "s"},
		{Server: "http://x", Token: "t"},
		{Server: "ftp://x", Token: "t", Stream: "s"},
	} {
		if _, err := New(o); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

func ioPipe() (*io.PipeReader, *io.PipeWriter) { return io.Pipe() }
