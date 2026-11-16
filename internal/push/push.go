// Package push is the client side of `cafard push`: it reads lines,
// batches them and posts them to a server's /api/log as NDJSON.
package push

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/textclean"
)

// Options configure a push.
type Options struct {
	Server string // base URL, e.g. http://10.0.50.2:8790
	Token  string
	Stream string
	// Echo, when set, also receives every line read.
	Echo io.Writer

	BatchEvery time.Duration // default 200 ms
	BatchMax   int           // default 1000 lines
	Log        *slog.Logger
	// Spool, when set, holds lines while the server is unreachable, and
	// what an earlier run left there is sent first.
	Spool *Spool
	// RetryMin and RetryMax bound the backoff; default 1 s and 30 s.
	RetryMin, RetryMax time.Duration
	// Version goes into the User-Agent.
	Version string
}

// Line is one line and when it was read.
type Line struct {
	Text string
	At   time.Time
}

// Pusher sends lines to the server.
type Pusher struct {
	opts     Options
	endpoint string
	client   *http.Client
	lines    chan Line
}

// New checks the options.
func New(opts Options) (*Pusher, error) {
	if opts.Server == "" {
		return nil, errors.New("--server (or CAFARD_SERVER) is required")
	}
	if opts.Token == "" {
		return nil, errors.New("--token (or CAFARD_TOKEN) is required")
	}
	if opts.Stream == "" {
		return nil, errors.New("--stream is required")
	}
	u, err := url.Parse(strings.TrimRight(opts.Server, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("--server %q must be an http:// or https:// URL", opts.Server)
	}
	if opts.BatchEvery <= 0 {
		opts.BatchEvery = 200 * time.Millisecond
	}
	if opts.BatchMax <= 0 {
		opts.BatchMax = 1000
	}
	if opts.RetryMin <= 0 {
		opts.RetryMin = time.Second
	}
	if opts.RetryMax < opts.RetryMin {
		opts.RetryMax = max(30*time.Second, opts.RetryMin)
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	q := url.Values{"stream": {opts.Stream}}
	return &Pusher{opts: opts, endpoint: u.String() + "/api/log?" + q.Encode(), client: &http.Client{Timeout: 60 * time.Second},
		lines: make(chan Line, 4*opts.BatchMax)}, nil
}

// Reader pushes everything read from r until EOF, then waits until every
// line is delivered. A partial line is sent after 500 ms of silence so a
// prompt is not held back.
func (p *Pusher) Reader(ctx context.Context, r io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, 1)
	go func() {
		err := p.send(ctx)
		errs <- err
		if err != nil {
			cancel() // unblock the reader: nothing will drain p.lines now
		}
	}()

	sp := textclean.NewSplitter(textclean.DefaultFlushAfter, textclean.MaxLineBytes, func(line string, at time.Time) {
		select {
		case p.lines <- Line{Text: line, At: at}:
		case <-ctx.Done():
		}
	})
	type chunk struct {
		b   []byte
		err error
	}
	chunks := make(chan chunk)
	go func() {
		for {
			buf := make([]byte, 32<<10)
			n, err := r.Read(buf)
			select {
			case chunks <- chunk{buf[:n], err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
read:
	for {
		select {
		case c := <-chunks:
			if len(c.b) > 0 {
				if p.opts.Echo != nil {
					_, _ = p.opts.Echo.Write(c.b)
				}
				sp.Write(c.b, time.Now())
			}
			if c.err != nil {
				if !errors.Is(c.err, io.EOF) {
					sp.Close()
					close(p.lines)
					<-errs
					return c.err
				}
				break read
			}
		case now := <-tick.C:
			sp.Tick(now)
		case <-ctx.Done():
			break read
		case err := <-errs:
			return err
		}
	}
	sp.Close()
	close(p.lines)
	return <-errs
}

type wireLine struct {
	Text string `json:"text"`
	TS   string `json:"ts"`
}

// batchHooks is what send and sendDirect do around the shared batching.
type batchHooks struct {
	// flush delivers (or spools) a full or timed-out batch.
	flush func(lines []Line) error
	// done runs once p.lines is closed and the last batch flushed.
	done func() error
	// stopped runs when ctx ends, with the batch not flushed yet.
	stopped func(batch []Line) error
	// retry, when set, is a timer the loop also waits on; replay runs
	// when it fires.
	retry  func() <-chan time.Time
	replay func() error
}

// batch reads p.lines into batches of up to BatchMax lines, each flushed
// when full or BatchEvery after its first line.
func (p *Pusher) batch(ctx context.Context, h batchHooks) error {
	var batch []Line
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		lines := batch
		batch = nil
		return h.flush(lines)
	}
	for {
		var retry <-chan time.Time
		if h.retry != nil {
			retry = h.retry()
		}
		select {
		case l, ok := <-p.lines:
			if !ok {
				if err := flush(); err != nil {
					return err
				}
				return h.done()
			}
			if len(batch) == 0 {
				timer.Reset(p.opts.BatchEvery)
			}
			batch = append(batch, l)
			if len(batch) >= p.opts.BatchMax {
				timer.Stop()
				if err := flush(); err != nil {
					return err
				}
			}
		case <-timer.C:
			if err := flush(); err != nil {
				return err
			}
		case <-retry:
			if err := h.replay(); err != nil {
				return err
			}
		case <-ctx.Done():
			return h.stopped(batch)
		}
	}
}

// backoff is how long to wait before the next attempt: the server's
// Retry-After when it gave one, else a jittered delay that doubles up to
// RetryMax.
func (p *Pusher) backoff(wait time.Duration, delay *time.Duration) time.Duration {
	if wait > 0 {
		return wait
	}
	wait = jitter(*delay)
	*delay = min(*delay*2, p.opts.RetryMax)
	return wait
}

// send batches lines from p.lines and posts them until the channel closes.
// With a spool, a batch the server cannot take goes to the spool, and so
// does everything after it until the spool is replayed, so order holds.
func (p *Pusher) send(ctx context.Context) error {
	if p.opts.Spool == nil {
		return p.sendDirect(ctx)
	}
	sp := p.opts.Spool
	var (
		retry <-chan time.Time
		down  bool
		delay = p.opts.RetryMin
	)
	if !sp.Empty() {
		p.opts.Log.Info("replaying lines left in the spool", "path", sp.Path(), "bytes", sp.Pending())
		retry = time.After(0)
	}
	failed := func(wait time.Duration, err error) {
		if !down {
			p.opts.Log.Warn("server unreachable, spooling lines", "path", sp.Path(), "err", err)
		}
		down = true
		retry = time.After(p.backoff(wait, &delay))
	}
	spool := func(lines []Line) error {
		dropped, err := sp.Append(lines)
		if dropped > 0 {
			p.opts.Log.Warn("spool full, lines dropped", "path", sp.Path(), "dropped", dropped)
		}
		if err != nil {
			return fmt.Errorf("spool %s: %w", sp.Path(), err)
		}
		if retry == nil {
			retry = time.After(0)
		}
		return nil
	}
	flush := func(lines []Line) error {
		if !down && sp.Empty() {
			wait, err := p.postOnce(ctx, lines)
			if err == nil {
				return nil
			}
			var perm *permanentError
			if errors.As(err, &perm) || ctx.Err() != nil {
				_ = spool(lines)
				return err
			}
			failed(wait, err)
		}
		return spool(lines)
	}
	replay := func() error {
		retry = nil
		lines, next, err := sp.Peek(p.opts.BatchMax)
		if err != nil {
			return fmt.Errorf("spool %s: %w", sp.Path(), err)
		}
		if len(lines) > 0 {
			wait, err := p.postOnce(ctx, lines)
			if err != nil {
				var perm *permanentError
				if errors.As(err, &perm) || ctx.Err() != nil {
					return err
				}
				failed(wait, err)
				return nil
			}
		}
		if down {
			p.opts.Log.Info("server reachable again, replaying the spool", "bytes", sp.Pending())
		}
		down, delay = false, p.opts.RetryMin
		if err := sp.Advance(next); err != nil {
			return fmt.Errorf("spool %s: %w", sp.Path(), err)
		}
		if !sp.Empty() {
			retry = time.After(0)
		}
		return nil
	}
	stopped := func(batch []Line) error {
		_ = spool(batch)
		if sp.Empty() {
			return ctx.Err()
		}
		p.opts.Log.Warn("lines wait in the spool for the next push", "path", sp.Path(), "bytes", sp.Pending())
		return fmt.Errorf("%w; %d bytes wait in %s for the next push", ctx.Err(), sp.Pending(), sp.Path())
	}
	return p.batch(ctx, batchHooks{
		flush: flush,
		// Input ended: deliver what the spool holds before leaving.
		done: func() error {
			for !sp.Empty() {
				if retry == nil {
					retry = time.After(0)
				}
				select {
				case <-retry:
					if err := replay(); err != nil {
						return err
					}
				case <-ctx.Done():
					return stopped(nil)
				}
			}
			return nil
		},
		stopped: stopped,
		retry:   func() <-chan time.Time { return retry },
		replay:  replay,
	})
}

// sendDirect is send without a spool: a batch is retried until delivered.
func (p *Pusher) sendDirect(ctx context.Context) error {
	return p.batch(ctx, batchHooks{
		flush: func(lines []Line) error { return p.post(ctx, lines) },
		done:  func() error { return nil },
		stopped: func(batch []Line) error {
			if len(batch) > 0 {
				return fmt.Errorf("stopped with %d lines not delivered", len(batch))
			}
			return ctx.Err()
		},
	})
}

func jitter(d time.Duration) time.Duration {
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// permanentError is a refusal that retrying cannot fix.
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

func encodeBatch(batch []Line) ([]byte, error) {
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	for _, l := range batch {
		if _, err := zw.Write(encodeLine(l)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

// postOnce sends one batch once. wait is the server's Retry-After.
func (p *Pusher) postOnce(ctx context.Context, batch []Line) (wait time.Duration, err error) {
	body, err := encodeBatch(batch)
	if err != nil {
		return 0, &permanentError{err.Error()}
	}
	return p.try(ctx, body)
}

// post sends one batch, retrying with backoff from 1 s to 30 s while the
// server is unreachable or busy.
func (p *Pusher) post(ctx context.Context, batch []Line) error {
	body, err := encodeBatch(batch)
	if err != nil {
		return err
	}
	delay := p.opts.RetryMin
	for attempt := 1; ; attempt++ {
		wait, err := p.try(ctx, body)
		if err == nil {
			if attempt > 1 {
				p.opts.Log.Info("server reachable again", "lines", len(batch))
			}
			return nil
		}
		var perm *permanentError
		if errors.As(err, &perm) {
			return err
		}
		wait = p.backoff(wait, &delay)
		p.opts.Log.Warn("push failed, retrying", "attempt", attempt, "in", wait.Round(time.Millisecond), "err", err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return fmt.Errorf("stopped with %d lines not delivered: %w", len(batch), err)
		}
	}
}

func (p *Pusher) try(ctx context.Context, body []byte) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, &permanentError{err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+p.opts.Token)
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("User-Agent", "cafard-push/"+p.opts.Version)
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusOK {
		return 0, nil
	}
	msg := serverMessage(data, resp.Status)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		var wait time.Duration
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
		return wait, errors.New(msg)
	default:
		return 0, &permanentError{fmt.Sprintf("the server refused the lines: %s", msg)}
	}
}

func serverMessage(body []byte, status string) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return status
}
