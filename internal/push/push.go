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
	Client     *http.Client
	Log        *slog.Logger
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
	lines    chan Line
	sent     int
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
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	q := url.Values{"stream": {opts.Stream}}
	return &Pusher{opts: opts, endpoint: u.String() + "/api/log?" + q.Encode(), lines: make(chan Line, 4*opts.BatchMax)}, nil
}

// Sent is how many lines the server accepted.
func (p *Pusher) Sent() int { return p.sent }

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

// send batches lines from p.lines and posts them until the channel closes.
func (p *Pusher) send(ctx context.Context) error {
	var batch []Line
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := p.post(ctx, batch); err != nil {
			return err
		}
		p.sent += len(batch)
		batch = batch[:0]
		return nil
	}
	for {
		select {
		case l, ok := <-p.lines:
			if !ok {
				return flush()
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
		case <-ctx.Done():
			if len(batch) > 0 {
				return fmt.Errorf("stopped with %d lines not delivered", len(batch))
			}
			return ctx.Err()
		}
	}
}

// permanentError is a refusal that retrying cannot fix.
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

// post sends one batch, retrying with backoff from 1 s to 30 s while the
// server is unreachable or busy.
func (p *Pusher) post(ctx context.Context, batch []Line) error {
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	enc := json.NewEncoder(zw)
	for _, l := range batch {
		if err := enc.Encode(wireLine{Text: l.Text, TS: l.At.UTC().Format(time.RFC3339Nano)}); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	delay := time.Second
	for attempt := 1; ; attempt++ {
		wait, err := p.try(ctx, body.Bytes())
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
		if wait <= 0 {
			wait = delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1))
			delay = min(delay*2, 30*time.Second)
		}
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
	resp, err := p.opts.Client.Do(req)
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
