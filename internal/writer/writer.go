// Package writer is the single goroutine that writes entries. Everything
// else hands entries to it, so the order of seq is the order of commits.
package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/store"
)

// Writer is how other packages add entries.
type Writer interface {
	// Submit queues an entry and returns at once. With lossy=true it never
	// blocks: when the queue is full it returns ErrFull and the caller
	// counts a drop. With lossy=false it blocks until queued or ctx ends.
	Submit(ctx context.Context, e store.Entry, lossy bool) error
	// Commit queues an entry and waits for its commit. It returns the seq.
	Commit(ctx context.Context, e store.Entry) (int64, error)
	// CommitBatch is Commit for several entries in one transaction.
	CommitBatch(ctx context.Context, es []store.Entry) (last int64, err error)
}

var (
	// ErrFull is a lossy submit refused because the queue is full.
	ErrFull = errors.New("writer queue full")
	// ErrClosed is returned once the writer has stopped.
	ErrClosed = errors.New("writer stopped")
)

// Publisher receives entries after they are committed.
type Publisher interface {
	Publish([]store.Entry)
}

// Seer is told about every committed entry (the status registry).
type Seer interface {
	Seen(stream string, ts time.Time)
}

// Options tune batching.
type Options struct {
	QueueSize  int           // default 8192 entries
	BatchMax   int           // default 500 entries
	BatchEvery time.Duration // default 50 ms
}

type result struct {
	last int64
	err  error
}

type request struct {
	entries []store.Entry
	reply   chan result // nil for Submit
	exec    func(ctx context.Context, tx *sql.Tx) error
}

// W is the writer.
type W struct {
	store *store.Store
	pub   Publisher
	seen  Seer
	clock clock.Clock
	log   *slog.Logger
	opts  Options

	queue   chan request
	stopped chan struct{}
	once    sync.Once
}

var _ Writer = (*W)(nil)

// New builds a writer; Run starts it.
func New(st *store.Store, pub Publisher, seen Seer, c clock.Clock, log *slog.Logger, opts Options) *W {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 8192
	}
	if opts.BatchMax <= 0 {
		opts.BatchMax = 500
	}
	if opts.BatchEvery <= 0 {
		opts.BatchEvery = 50 * time.Millisecond
	}
	return &W{store: st, pub: pub, seen: seen, clock: c, log: log, opts: opts,
		queue: make(chan request, opts.QueueSize), stopped: make(chan struct{})}
}

// Prepare fills defaults and checks an entry. It is exported so callers can
// reject bad input before queueing it.
func (w *W) Prepare(e *store.Entry) error {
	if err := store.ValidateStreamID(e.Stream); err != nil {
		return err
	}
	if e.Kind == "" {
		e.Kind = store.KindLine
	}
	if !e.Kind.Valid() {
		return fmt.Errorf("entry kind %q is not line, marker or message", e.Kind)
	}
	if e.Kind == store.KindMessage && !store.IsMessageStream(e.Stream) {
		return fmt.Errorf("a message must go in a chan: or dm: stream, not %s", e.Stream)
	}
	if e.Kind != store.KindMessage && store.IsMessageStream(e.Stream) && e.Kind != store.KindMarker {
		return fmt.Errorf("stream %s holds messages and markers only", e.Stream)
	}
	if e.Severity < 0 || e.Severity > 7 {
		return fmt.Errorf("severity %d is not 0 to 7", e.Severity)
	}
	if len(e.Meta) > store.MaxMetaBytes {
		return fmt.Errorf("meta is %d bytes; the limit is %d", len(e.Meta), store.MaxMetaBytes)
	}
	if len(e.Meta) > 0 && !json.Valid(e.Meta) {
		return errors.New("meta is not valid JSON")
	}
	if !utf8.ValidString(e.Text) {
		return errors.New("text is not valid UTF-8")
	}
	if e.TS.IsZero() {
		e.TS = w.clock.Now()
	}
	e.TS = e.TS.UTC()
	e.Seq = 0
	return nil
}

func (w *W) send(ctx context.Context, req request, lossy bool) error {
	select {
	case <-w.stopped:
		return ErrClosed
	default:
	}
	if lossy {
		select {
		case w.queue <- req:
			return nil
		default:
			return ErrFull
		}
	}
	select {
	case w.queue <- req:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-w.stopped:
		return ErrClosed
	}
}

func (w *W) wait(ctx context.Context, reply chan result) (int64, error) {
	select {
	case r := <-reply:
		return r.last, r.err
	case <-ctx.Done():
		// The entry may still commit; the caller just stops waiting.
		return 0, ctx.Err()
	case <-w.stopped:
		select {
		case r := <-reply:
			return r.last, r.err
		default:
			return 0, ErrClosed
		}
	}
}

func (w *W) Submit(ctx context.Context, e store.Entry, lossy bool) error {
	if err := w.Prepare(&e); err != nil {
		return err
	}
	return w.send(ctx, request{entries: []store.Entry{e}}, lossy)
}

func (w *W) Commit(ctx context.Context, e store.Entry) (int64, error) {
	return w.CommitBatch(ctx, []store.Entry{e})
}

func (w *W) CommitBatch(ctx context.Context, es []store.Entry) (int64, error) {
	if len(es) == 0 {
		return 0, nil
	}
	prepared := make([]store.Entry, len(es))
	for i, e := range es {
		if err := w.Prepare(&e); err != nil {
			return 0, err
		}
		prepared[i] = e
	}
	reply := make(chan result, 1)
	if err := w.send(ctx, request{entries: prepared, reply: reply}, false); err != nil {
		return 0, err
	}
	return w.wait(ctx, reply)
}

// Exec runs fn in the writer goroutine, between batches, in its own
// transaction. The janitor deletes entries through it, so deletions are
// serialised with inserts like everything else.
func (w *W) Exec(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error {
	reply := make(chan result, 1)
	if err := w.send(ctx, request{exec: fn, reply: reply}, false); err != nil {
		return err
	}
	_, err := w.wait(ctx, reply)
	return err
}

// Run writes until ctx ends, then commits what is queued and returns.
func (w *W) Run(ctx context.Context) error {
	defer w.once.Do(func() { close(w.stopped) })
	var (
		batch   []request
		count   int
		timer   *time.Timer
		timerCh <-chan time.Time
	)
	flush := func() {
		if len(batch) > 0 {
			w.commit(batch)
		}
		batch, count = batch[:0], 0
		if timer != nil {
			timer.Stop()
			timer, timerCh = nil, nil
		}
	}
	handle := func(req request) {
		if req.exec != nil {
			flush()
			w.runExec(req)
			return
		}
		batch = append(batch, req)
		count += len(req.entries)
		if count >= w.opts.BatchMax {
			flush()
		} else if timer == nil {
			timer = time.NewTimer(w.opts.BatchEvery)
			timerCh = timer.C
		}
	}
	for {
		select {
		case req := <-w.queue:
			handle(req)
		case <-timerCh:
			timer, timerCh = nil, nil
			flush()
		case <-ctx.Done():
			for {
				select {
				case req := <-w.queue:
					handle(req)
					continue
				default:
				}
				break
			}
			flush()
			return nil
		}
	}
}

func (w *W) runExec(req request) {
	ctx := context.Background()
	tx, err := w.store.DB().BeginTx(ctx, nil)
	if err == nil {
		err = req.exec(ctx, tx)
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	req.reply <- result{err: err}
}

func (w *W) commit(batch []request) {
	var all []store.Entry
	for _, r := range batch {
		all = append(all, r.entries...)
	}
	committed, err := w.store.WriteBatch(context.Background(), all)
	if err != nil {
		w.log.Error("committing entries failed", "entries", len(all), "err", err)
		for _, r := range batch {
			if r.reply != nil {
				r.reply <- result{err: err}
			}
		}
		return
	}
	i := 0
	for _, r := range batch {
		n := len(r.entries)
		if r.reply != nil {
			r.reply <- result{last: committed[i+n-1].Seq}
		}
		i += n
	}
	if w.seen != nil {
		for _, e := range committed {
			w.seen.Seen(e.Stream, e.TS)
		}
	}
	if w.pub != nil {
		w.pub.Publish(committed)
	}
}
