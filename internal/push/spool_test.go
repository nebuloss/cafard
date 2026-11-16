package push

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/ingest/serial"
	"github.com/nebuloss/cafard/internal/testutil"
)

func spoolOpts(t *testing.T, url, path string) Options {
	sp, err := OpenSpool(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sp.Close() })
	return Options{Server: url, Token: "cfd_x", Stream: "laptop", Spool: sp, BatchEvery: 5 * time.Millisecond,
		BatchMax: 7, RetryMin: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond}
}

func TestSpoolHoldsLinesWhileTheServerIsDown(t *testing.T) {
	f := &fakeServer{down: true}
	srv := httptest.NewServer(f)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "spool", "laptop.ndjson")
	p, err := New(spoolOpts(t, srv.URL, path))
	if err != nil {
		t.Fatal(err)
	}
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- p.Reader(context.Background(), r) }()

	for i := range 30 {
		fmt.Fprintf(w, "line %d\n", i)
	}
	testutil.Eventually(t, "the spool file", 5*time.Second, func() bool { fi, err := os.Stat(path); return err == nil && fi.Size() > 0 })
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("spool mode %v", fi.Mode().Perm())
	}
	downUntil := time.Now()
	f.set(false)
	for i := 30; i < 60; i++ {
		fmt.Fprintf(w, "line %d\n", i)
	}
	w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := f.texts()
	if len(got) != 60 {
		t.Fatalf("server got %d lines: %q", len(got), got)
	}
	for i, s := range got {
		if s != fmt.Sprintf("line %d", i) {
			t.Fatalf("line %d is %q: order lost", i, s)
		}
	}
	// Spooled lines keep the time they were read, not the time they were sent.
	ts, err := time.Parse(time.RFC3339Nano, f.lines[0].TS)
	if err != nil || ts.After(downUntil) {
		t.Errorf("first line ts %v (err %v), server came back at %v", ts, err, downUntil)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("spool left behind: %v", err)
	}
	if _, err := os.Stat(path + ".pos"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("spool offset left behind: %v", err)
	}
}

func TestLeftoversFromAnEarlierRunGoFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "laptop.ndjson")
	sp, err := OpenSpool(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	if _, err := sp.Append([]Line{{"old 1", old}, {"old 2", old}, {"old 3", old}}); err != nil {
		t.Fatal(err)
	}
	// One was delivered before the earlier run stopped.
	_, next, _ := sp.Peek(1)
	if err := sp.Advance(next); err != nil {
		t.Fatal(err)
	}
	sp.Close()

	f := &fakeServer{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	p, _ := New(spoolOpts(t, srv.URL, path))
	if err := p.Reader(context.Background(), strings.NewReader("new\n")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.texts(), ","); got != "old 2,old 3,new" {
		t.Errorf("server got %s", got)
	}
	if f.lines[0].TS != "2026-11-01T08:00:00Z" {
		t.Errorf("ts %q", f.lines[0].TS)
	}
}

func TestRefusalKeepsTheLinesSpooled(t *testing.T) {
	f := &fakeServer{status: http.StatusUnauthorized}
	srv := httptest.NewServer(f)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "laptop.ndjson")
	p, _ := New(spoolOpts(t, srv.URL, path))
	err := p.Reader(context.Background(), strings.NewReader("a\nb\n"))
	if err == nil || !strings.Contains(err.Error(), "The token is revoked.") {
		t.Fatalf("err = %v", err)
	}
	sp, _ := OpenSpool(path, 0)
	defer sp.Close()
	lines, _, _ := sp.Peek(10)
	if len(lines) != 2 || lines[0].Text != "a" {
		t.Errorf("spool holds %+v", lines)
	}
}

func TestSpoolCapDropsAndSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.ndjson")
	sp, err := OpenSpool(path, 600)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	var lines []Line
	for i := range 20 {
		lines = append(lines, Line{fmt.Sprintf("line %02d", i), at}) // 47 bytes each: 12 fit
	}
	dropped, err := sp.Append(lines)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 8 || sp.Pending() > 600 {
		t.Fatalf("dropped %d, pending %d", dropped, sp.Pending())
	}
	// Deliver four, then append: the delivered head is compacted away and
	// the note comes before the first new line that fits.
	got, next, _ := sp.Peek(4)
	if len(got) != 4 || got[0].Text != "line 00" {
		t.Fatalf("peek %+v", got)
	}
	if err := sp.Advance(next); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Append([]Line{{"after", at}}); err != nil {
		t.Fatal(err)
	}
	all, _, _ := sp.Peek(100)
	var texts []string
	for _, l := range all {
		texts = append(texts, l.Text)
	}
	want := "line 04,line 05,line 06,line 07,line 08,line 09,line 10,line 11,cafard push: the spool was full (0 MiB), 8 lines were dropped here,after"
	if strings.Join(texts, ",") != want {
		t.Errorf("spool holds\n%s\nwant\n%s", strings.Join(texts, ","), want)
	}
	if fi, _ := os.Stat(path); fi.Size() > 600 {
		t.Errorf("file is %d bytes, cap 600", fi.Size())
	}
}

func TestSpoolCutsAHalfWrittenLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.ndjson")
	os.WriteFile(path, []byte(`{"text":"whole","ts":"2026-11-16T09:00:00Z"}`+"\n"+`{"text":"ha`), 0o600)
	sp, err := OpenSpool(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	if _, err := sp.Append([]Line{{"next", time.Now()}}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := sp.Peek(10)
	if len(got) != 2 || got[0].Text != "whole" || got[1].Text != "next" {
		t.Errorf("%+v", got)
	}
}

func TestDefaultSpoolPathPerServerAndStream(t *testing.T) {
	env := map[string]string{"HOME": "/home/u", "LocalAppData": `C:\Users\u\AppData\Local`}
	a, err := DefaultSpoolPath(func(k string) string { return env[k] }, "http://10.0.50.2:8790", "bench/1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := DefaultSpoolPath(func(k string) string { return env[k] }, "http://10.0.50.3:8790", "bench/1")
	if a == b || !strings.Contains(a, "bench_1-") || !strings.Contains(a, filepath.Join("cafard", "spool")) {
		t.Errorf("%s / %s", a, b)
	}
	if _, err := DefaultSpoolPath(func(string) string { return "" }, "http://x", "s"); err == nil {
		t.Error("no home, no error")
	}
}

// collect reads r into a string that tests poll.
type collect struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (c *collect) run(r io.Reader) {
	b := make([]byte, 256)
	for {
		n, err := r.Read(b)
		c.mu.Lock()
		c.buf.Write(b[:n])
		c.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (c *collect) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

func TestFollowSurvivesTruncationAndRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "putty.log")
	appendFile(t, path, "before start\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := Follow(ctx, path, FollowOptions{Poll: 5 * time.Millisecond})
	defer r.Close()
	c := &collect{}
	go c.run(r)

	time.Sleep(30 * time.Millisecond) // let it open and seek to the end
	appendFile(t, path, "one\n")
	testutil.Eventually(t, "one", 5*time.Second, func() bool { return c.String() == "one\n" })

	// Truncated, as PuTTY does when it overwrites the log.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	appendFile(t, path, "two\n")
	testutil.Eventually(t, "two", 5*time.Second, func() bool { return c.String() == "one\ntwo\n" })

	// Rotated: the old file gets a last line, then a new file takes its name.
	appendFile(t, path, "last of old\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, "first of new\n")
	testutil.Eventually(t, "rotation", 5*time.Second, func() bool { return c.String() == "one\ntwo\nlast of old\nfirst of new\n" })

	cancel()
	testutil.Eventually(t, "stop", 5*time.Second, func() bool { _, err := r.Read(make([]byte, 1)); return errors.Is(err, context.Canceled) })
}

func TestFollowWaitsForTheFileAndReadsItAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "later.log")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := Follow(ctx, path, FollowOptions{Poll: 5 * time.Millisecond})
	c := &collect{}
	go c.run(r)
	time.Sleep(20 * time.Millisecond)
	appendFile(t, path, "hello\n")
	testutil.Eventually(t, "hello", 5*time.Second, func() bool { return c.String() == "hello\n" })
}

func TestFollowFromStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.log")
	appendFile(t, path, "kept\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &collect{}
	go c.run(Follow(ctx, path, FollowOptions{FromStart: true, Poll: 5 * time.Millisecond}))
	testutil.Eventually(t, "kept", 5*time.Second, func() bool { return c.String() == "kept\n" })
}

func TestSerialReaderReopensALostPort(t *testing.T) {
	ends := make(chan net.Conn, 4)
	var mu sync.Mutex
	opens := 0
	fail := true
	open := func(p serial.Port) (io.ReadCloser, error) {
		mu.Lock()
		defer mu.Unlock()
		opens++
		if p.Device != "COM5" || p.Baud != 9600 {
			return nil, fmt.Errorf("wrong port %+v", p)
		}
		if fail {
			return nil, errors.New("The system cannot find the file specified.")
		}
		a, b := net.Pipe()
		ends <- b
		return a, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := SerialOptions{Port: serial.Port{Device: "COM5", Baud: 9600}, Open: open, MinBackoff: 5 * time.Millisecond, MaxBackoff: 10 * time.Millisecond}
	if _, err := SerialReader(ctx, opts); err == nil {
		t.Fatal("a missing port at start is not an error")
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	r, err := SerialReader(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	c := &collect{}
	go c.run(r)
	b := <-ends
	b.Write([]byte("boot\n"))
	testutil.Eventually(t, "boot", 5*time.Second, func() bool { return c.String() == "boot\n" })

	// Unplugged: the next opens fail for a while, then it is back.
	mu.Lock()
	fail = true
	mu.Unlock()
	b.Close()
	testutil.Eventually(t, "retries", 5*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return opens >= 5 })
	mu.Lock()
	fail = false
	mu.Unlock()
	b = <-ends
	b.Write([]byte("again\n"))
	testutil.Eventually(t, "again", 5*time.Second, func() bool { return c.String() == "boot\nagain\n" })
}
