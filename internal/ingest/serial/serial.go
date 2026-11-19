// Package serial reads serial consoles into streams: local ports, raw TCP
// serial servers and RFC 2217 servers, with reconnect and markers. It also
// lets other parts write to a port and drive its lines (Manager.Lookup).
package serial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
)

// Options tune a Manager; the zero value is what the server runs.
type Options struct {
	Factory    Factory       // default Open
	MinBackoff time.Duration // default 1 s
	MaxBackoff time.Duration // default 30 s
	TickEvery  time.Duration // how often a partial line is checked, default 100 ms
}

// Manager runs the serial ports of the sinks, and follows their changes.
type Manager struct {
	d    deps.Deps
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	ctx      context.Context // the run context, once Run started
	byStream map[string]*Handle
	wg       sync.WaitGroup
}

// Register runs the ports of the sinks and adds the serial_write and
// serial_control tools; start reads the ports until the context ends.
// The tools are there even before any sink has a port: one may get one
// at run time, and tools cannot be added once /mcp is mounted.
func Register(_ *http.ServeMux, s *mcpserver.Server, d deps.Deps) (func(context.Context) error, error) {
	m := newManager(d, Options{})
	m.Reconcile(d.Projects.SerialPorts())
	d.Projects.OnSinksChanged(func() { m.Reconcile(d.Projects.SerialPorts()) })
	if s != nil {
		registerTools(s, m)
	}
	return m.Run, nil
}

// New checks the ports and declares their streams.
func New(d deps.Deps, cfg *Config, opts Options) (*Manager, error) {
	c := Config{Ports: append([]Port(nil), cfg.Ports...)}
	c.SetDefaults()
	if err := c.validate(true); err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	m := newManager(d, opts)
	for _, p := range c.Ports {
		m.add(p)
	}
	return m, nil
}

func newManager(d deps.Deps, opts Options) *Manager {
	if opts.Factory == nil {
		opts.Factory = Open
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = time.Second
	}
	if opts.MaxBackoff < opts.MinBackoff {
		opts.MaxBackoff = max(30*time.Second, opts.MinBackoff)
	}
	if opts.TickEvery <= 0 {
		opts.TickEvery = 100 * time.Millisecond
	}
	log := d.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Manager{d: d, opts: opts, log: log.With("component", "serial"), byStream: map[string]*Handle{}}
}

// add declares p and, once Run started, opens it. m.mu is held, or Run
// has not started.
func (m *Manager) add(p Port) {
	h := &Handle{m: m, port: p, stream: p.Stream(), transport: p.Transport(), writable: p.Writable, baud: p.Baud,
		log: m.log.With("stream", p.Stream())}
	m.byStream[h.stream] = h
	m.d.Status.Declare(status.Source{Stream: h.stream, Transport: h.transport})
	if m.ctx != nil && m.ctx.Err() == nil {
		m.start(h)
	}
}

func (m *Manager) start(h *Handle) {
	ctx, cancel := context.WithCancel(m.ctx)
	h.cancel, h.done = cancel, make(chan struct{})
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(h.done)
		h.run(ctx)
	}()
}

// Reconcile makes the running ports those of ports: a new one opens, one
// that is gone stops with a marker saying so, a changed one reopens with
// its new settings. A port that does not validate is logged and skipped.
func (m *Manager) Reconcile(ports []Port) {
	want := map[string]Port{}
	for _, p := range ports {
		p.SetDefaults()
		if err := p.Validate(); err != nil {
			m.log.Error("serial port skipped", "err", err)
			continue
		}
		want[p.Stream()] = p
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for stream, h := range m.byStream {
		p, keep := want[stream]
		if keep && p == h.port {
			continue
		}
		reason := "port removed"
		if keep {
			reason = "port settings changed"
		}
		if h.cancel != nil {
			h.cancel()
			<-h.done
		}
		delete(m.byStream, stream)
		m.d.Status.SetConnected(stream, false, reason)
		h.marker(nil, "removed", reason, store.DefaultSeverity)
		h.log.Info("serial port stopped", "reason", reason)
	}
	for stream, p := range want {
		if _, ok := m.byStream[stream]; !ok {
			m.add(p)
		}
	}
}

// Run reads every port until ctx ends.
func (m *Manager) Run(ctx context.Context) error {
	m.mu.Lock()
	m.ctx = ctx
	for _, h := range m.byStream {
		m.start(h)
	}
	m.mu.Unlock()
	<-ctx.Done()
	m.wg.Wait()
	return nil
}

// Lookup finds a port by stream id ("serial:lab/dut1").
func (m *Manager) Lookup(stream string) (*Handle, error) {
	m.mu.Lock()
	h, ok := m.byStream[stream]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownStream, stream)
	}
	return h, nil
}

// Streams are the streams of the running ports, sorted.
func (m *Manager) Streams() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.byStream))
	for st := range m.byStream {
		out = append(out, st)
	}
	sort.Strings(out)
	return out
}

// Handle is one port at run time.
type Handle struct {
	m      *Manager
	log    *slog.Logger
	ctx    context.Context // the run context, for markers from link callbacks
	cancel context.CancelFunc
	done   chan struct{} // closed when run returns
	ctl    sync.Mutex    // one Write or Control at a time

	port              Port // as configured, never changed
	stream, transport string
	writable          bool

	mu   sync.Mutex
	baud int // follows Control, so a reconnect keeps it
	link io.ReadWriteCloser
	down string // why the port is not connected
	ls   status.LineState
}

// Stream is "serial:<name>".
func (h *Handle) Stream() string { return h.stream }

// Transport is local, rfc2217 or tcp.
func (h *Handle) Transport() string { return h.transport }

// Writable is the port's `writable` setting.
func (h *Handle) Writable() bool { return h.writable }

// LineState is the last known state of the modem lines.
func (h *Handle) LineState() status.LineState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return copyLineState(h.ls)
}

// Connected reports whether the port is open, and why not.
func (h *Handle) Connected() (bool, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.link != nil, h.down
}

func copyLineState(ls status.LineState) status.LineState {
	cp := func(b *bool) *bool {
		if b == nil {
			return nil
		}
		v := *b
		return &v
	}
	return status.LineState{DTR: cp(ls.DTR), RTS: cp(ls.RTS), CTS: cp(ls.CTS), DCD: cp(ls.DCD)}
}

// setLines merges the known fields of ls into the line state.
func (h *Handle) setLines(ls status.LineState) {
	h.mu.Lock()
	if ls.DTR != nil {
		h.ls.DTR = ls.DTR
	}
	if ls.RTS != nil {
		h.ls.RTS = ls.RTS
	}
	if ls.CTS != nil {
		h.ls.CTS = ls.CTS
	}
	if ls.DCD != nil {
		h.ls.DCD = ls.DCD
	}
	cur := copyLineState(h.ls)
	h.mu.Unlock()
	h.m.d.Status.SetLineState(h.Stream(), cur)
}

// events adapts a Handle to Events without exporting a second LineState
// method next to the getter.
type events struct{ h *Handle }

func (e events) Marker(text string, severity int) { e.h.marker(e.h.ctx, "line", text, severity) }
func (e events) LineState(ls status.LineState)    { e.h.setLines(ls) }

func (h *Handle) marker(ctx context.Context, event, text string, severity int) {
	meta, _ := json.Marshal(map[string]string{"serial": event})
	e := store.Entry{Stream: h.Stream(), Kind: store.KindMarker, Severity: severity, Text: "serial: " + text, Meta: meta}
	if ctx == nil || ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		defer cancel()
	}
	if err := h.m.d.Writer.Submit(ctx, e, false); err != nil {
		h.log.Warn("marker lost", "err", err)
	}
}

func (h *Handle) current() Port {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.port
	p.Baud = h.baud
	return p
}

func (h *Handle) setLink(l io.ReadWriteCloser, down string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.link, h.down = l, down
	if l == nil {
		// DTR and RTS are whatever the next open makes them.
		h.ls.DTR, h.ls.RTS = nil, nil
	}
}

// run opens the port, reads it, and reopens it with backoff until ctx ends.
func (h *Handle) run(ctx context.Context) {
	h.ctx = ctx
	st := h.m.d.Status
	backoff := h.m.opts.MinBackoff
	var (
		everUp   bool
		reported bool // a marker already says the port is down
		downAt   time.Time
	)
	for ctx.Err() == nil {
		p := h.current()
		link, err := h.m.opts.Factory(ctx, p, events{h})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			reason := err.Error()
			h.setLink(nil, reason)
			st.SetConnected(h.Stream(), false, reason)
			if !reported {
				reported = true
				h.log.Warn("cannot open serial port", "addr", p.Address(), "err", err)
				h.marker(ctx, "open_failed", fmt.Sprintf("cannot open %s: %s; retrying", p.Address(), reason), 4)
			}
			if !h.sleep(ctx, backoff) {
				return
			}
			backoff = min(2*backoff, h.m.opts.MaxBackoff)
			continue
		}

		desc := p.Address()
		if d, ok := link.(describer); ok {
			desc = d.Describe()
		}
		h.setLink(link, "")
		st.SetConnected(h.Stream(), true, "")
		if everUp {
			h.marker(ctx, "reconnected", fmt.Sprintf("reconnected after %s: %s", h.m.d.Clock.Now().Sub(downAt).Round(time.Second), desc), store.DefaultSeverity)
		} else {
			h.marker(ctx, "connected", "connected: "+desc, store.DefaultSeverity)
		}
		h.log.Info("serial port open", "addr", p.Address())
		everUp, reported = true, false
		openedAt := h.m.d.Clock.Now()

		err = h.pump(ctx, link)
		reason := "closed"
		if err != nil {
			reason = err.Error()
		}
		if errors.Is(err, io.EOF) {
			reason = "the far end closed the connection"
		}
		h.setLink(nil, reason)
		link.Close()
		if ctx.Err() != nil {
			return
		}
		downAt = h.m.d.Clock.Now()
		st.SetConnected(h.Stream(), false, reason)
		h.log.Warn("serial port lost", "addr", p.Address(), "err", reason)
		h.marker(ctx, "disconnected", fmt.Sprintf("disconnected from %s: %s", p.Address(), reason), 4)
		reported = true
		if downAt.Sub(openedAt) > h.m.opts.MaxBackoff {
			backoff = h.m.opts.MinBackoff
		}
		if !h.sleep(ctx, backoff) {
			return
		}
		backoff = min(2*backoff, h.m.opts.MaxBackoff)
	}
}

func (h *Handle) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type chunk struct {
	b   []byte
	err error
}

// pump reads link into lines until it fails or ctx ends.
func (h *Handle) pump(ctx context.Context, link io.Reader) error {
	done := make(chan struct{})
	defer close(done)
	chunks := make(chan chunk, 16)
	go func() {
		for {
			buf := make([]byte, 4096)
			n, err := link.Read(buf)
			if n == 0 && err == nil {
				continue
			}
			select {
			case chunks <- chunk{buf[:n], err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	stream := h.Stream()
	sp := textclean.NewSplitter(textclean.DefaultFlushAfter, textclean.MaxLineBytes, func(line string, at time.Time) {
		sctx := ctx
		if ctx.Err() != nil {
			// The last partial line at shutdown: the writer drains after
			// the readers stop, so give it a moment.
			var cancel context.CancelFunc
			sctx, cancel = context.WithTimeout(context.Background(), time.Second)
			defer cancel()
		}
		_ = h.m.d.Writer.Submit(sctx, store.Entry{Stream: stream, Kind: store.KindLine, Severity: store.DefaultSeverity, Text: line, TS: at}, false)
	})
	defer sp.Close()
	tick := time.NewTicker(h.m.opts.TickEvery)
	defer tick.Stop()
	for {
		select {
		case c := <-chunks:
			if len(c.b) > 0 {
				sp.Write(c.b, h.m.d.Clock.Now())
			}
			if c.err != nil {
				return c.err
			}
		case <-tick.C:
			sp.Tick(h.m.d.Clock.Now())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
