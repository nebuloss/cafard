package writer_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/writer"
)

type seen struct {
	mu sync.Mutex
	n  map[string]int
}

func (s *seen) Seen(stream string, _ time.Time) {
	s.mu.Lock()
	s.n[stream]++
	s.mu.Unlock()
}

type fixture struct {
	st   *store.Store
	hub  *hub.Memory
	w    *writer.W
	seen *seen
	stop func()
	done chan error
}

func start(t *testing.T, opts writer.Options) *fixture {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(nil)
	sn := &seen{n: map[string]int{}}
	w := writer.New(st, h, sn, clock.System{}, slog.New(slog.NewTextHandler(io.Discard, nil)), opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	f := &fixture{st: st, hub: h, w: w, seen: sn, stop: cancel, done: done}
	t.Cleanup(func() {
		cancel()
		<-done
		st.Close()
	})
	return f
}

func TestCommitReturnsIncreasingSeqs(t *testing.T) {
	f := start(t, writer.Options{})
	ctx := context.Background()
	a, err := f.w.Commit(ctx, store.Entry{Stream: "push:a", Text: "one"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.w.CommitBatch(ctx, []store.Entry{{Stream: "push:a", Text: "two"}, {Stream: "push:b", Text: "three"}})
	if err != nil {
		t.Fatal(err)
	}
	if a != 1 || b != 3 {
		t.Errorf("seqs = %d, %d", a, b)
	}
	e, err := f.st.Get(ctx, 3)
	if err != nil || e.Text != "three" || e.Kind != store.KindLine {
		t.Errorf("entry = %+v, %v", e, err)
	}
}

func TestPrepareRejectsBadEntries(t *testing.T) {
	f := start(t, writer.Options{})
	bad := []store.Entry{
		{Stream: "nope", Text: "x"},
		{Stream: "push:a", Text: "x", Severity: 9},
		{Stream: "push:a", Text: "x", Kind: "weird"},
		{Stream: "push:a", Text: "x", Kind: store.KindMessage},
		{Stream: "chan:general", Text: "x", Kind: store.KindLine},
		{Stream: "push:a", Text: "x", Meta: []byte("{bad")},
		{Stream: "push:a", Text: "\xff"},
	}
	for _, e := range bad {
		if _, err := f.w.Commit(context.Background(), e); err == nil {
			t.Errorf("%+v: want an error", e)
		}
	}
}

func TestPublishesAfterCommitInSeqOrder(t *testing.T) {
	f := start(t, writer.Options{BatchEvery: 5 * time.Millisecond})
	sub := f.hub.Subscribe(hub.Filter{MinSeverity: 7}, 2000)
	defer sub.Close()
	ctx := context.Background()
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 250 {
				if err := f.w.Submit(ctx, store.Entry{Stream: "push:a", Text: "x", Severity: g}, false); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	var last int64
	for range 1000 {
		select {
		case e := <-sub.C():
			if e.Seq <= last {
				t.Fatalf("seq %d after %d", e.Seq, last)
			}
			last = e.Seq
			if _, err := f.st.Get(ctx, e.Seq); err != nil {
				t.Fatalf("published seq %d is not in the store: %v", e.Seq, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only got up to seq %d", last)
		}
	}
	f.seen.mu.Lock()
	n := f.seen.n["push:a"]
	f.seen.mu.Unlock()
	if n != 1000 {
		t.Errorf("status saw %d entries, want 1000", n)
	}
}

func TestLossySubmitReportsFull(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Not running: nothing drains the queue.
	w := writer.New(st, nil, nil, clock.System{}, slog.New(slog.NewTextHandler(io.Discard, nil)), writer.Options{QueueSize: 2})
	ctx := context.Background()
	for range 2 {
		if err := w.Submit(ctx, store.Entry{Stream: "syslog:x", Text: "x"}, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Submit(ctx, store.Entry{Stream: "syslog:x", Text: "x"}, true); !errors.Is(err, writer.ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := w.Submit(short, store.Entry{Stream: "syslog:x", Text: "x"}, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocking submit err = %v", err)
	}
}

func TestShutdownCommitsWhatIsQueued(t *testing.T) {
	f := start(t, writer.Options{BatchEvery: time.Hour, BatchMax: 1 << 20})
	ctx := context.Background()
	for range 10 {
		if err := f.w.Submit(ctx, store.Entry{Stream: "push:a", Text: "x"}, false); err != nil {
			t.Fatal(err)
		}
	}
	f.stop()
	if err := <-f.done; err != nil {
		t.Fatal(err)
	}
	f.done <- nil // for the cleanup
	last, _ := f.st.LastSeq(ctx)
	if last != 10 {
		t.Errorf("committed %d entries before stopping, want 10", last)
	}
	if _, err := f.w.Commit(ctx, store.Entry{Stream: "push:a", Text: "late"}); !errors.Is(err, writer.ErrClosed) {
		t.Errorf("commit after stop err = %v", err)
	}
}

func TestExecRunsBetweenBatches(t *testing.T) {
	f := start(t, writer.Options{})
	ctx := context.Background()
	if _, err := f.w.Commit(ctx, store.Entry{Stream: "push:a", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	err := f.w.Exec(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM entries`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Get(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("entry still there: %v", err)
	}
	failing := errors.New("nope")
	if err := f.w.Exec(ctx, func(context.Context, *sql.Tx) error { return failing }); !errors.Is(err, failing) {
		t.Errorf("exec err = %v", err)
	}
}

func TestHubDropsALaggingSubscriber(t *testing.T) {
	f := start(t, writer.Options{})
	slow := f.hub.Subscribe(hub.Filter{MinSeverity: 7}, 1)
	defer slow.Close()
	if _, err := f.w.CommitBatch(context.Background(), []store.Entry{{Stream: "push:a", Text: "1"}, {Stream: "push:a", Text: "2"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-slow.Lagged():
	case <-time.After(2 * time.Second):
		t.Fatal("lagging subscriber was not told")
	}
	if f.hub.Subscribers() != 0 {
		t.Errorf("lagged subscriber still registered")
	}
}
