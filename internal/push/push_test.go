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

	"github.com/nebuloss/cafard/internal/testutil"
)

// fakeServer is /api/log for stream "laptop". It records the lines it
// accepts, the batches and the Authorization header. It answers 503 with
// Retry-After: 1 to the first fail requests, 503 without it while down,
// and a refusal with status when that is set.
type fakeServer struct {
	mu      sync.Mutex
	fail    int
	down    bool
	status  int
	lines   []wireLine
	batches int
	auth    string
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = r.Header.Get("Authorization")
	if f.status != 0 {
		http.Error(w, `{"error":{"code":"unauthorized","message":"The token is revoked."}}`, f.status)
		return
	}
	if f.fail > 0 {
		f.fail--
		w.Header().Set("Retry-After", "1")
		http.Error(w, `{"error":{"code":"busy","message":"busy"}}`, http.StatusServiceUnavailable)
		return
	}
	if f.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
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
		f.lines = append(f.lines, l)
	}
	f.batches++
	w.Write([]byte(`{"accepted":1,"last_seq":1}`))
}

func (f *fakeServer) set(down bool) {
	f.mu.Lock()
	f.down = down
	f.mu.Unlock()
}

func (f *fakeServer) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, l := range f.lines {
		out = append(out, l.Text)
	}
	return out
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
	got := f.texts()
	if len(got) != 26 || got[25] != "no newline" || got[3] != "xxx" {
		t.Fatalf("server got %d lines: %q", len(got), got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.batches < 3 {
		t.Errorf("%d batches, want at least 3 of at most 10 lines", f.batches)
	}
	if f.auth != "Bearer cfd_x" {
		t.Errorf("auth = %q", f.auth)
	}
	if time.Since(start) < time.Second {
		t.Error("the 503 was not retried after Retry-After")
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
	testutil.Eventually(t, "the prompt", 3*time.Second, func() bool { return len(f.texts()) == 1 })
	w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := f.texts(); got[0] != "login: " {
		t.Errorf("line = %q", got[0])
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
