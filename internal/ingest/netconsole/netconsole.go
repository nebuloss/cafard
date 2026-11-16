// Package netconsole receives the Linux kernel log over UDP, in the plain
// and the extended netconsole formats.
package netconsole

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/ingest/ingestutil"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
	"github.com/nebuloss/cafard/internal/writer"
)

// DefaultAddr is the netconsole port.
const DefaultAddr = ":6666"

// Config is the `netconsole:` config section.
type Config struct {
	// UDP is the listen address; empty disables netconsole.
	UDP string `yaml:"udp"`
	// Names maps source IP addresses to stream names.
	Names map[string]string `yaml:"names,omitempty"`
}

// SetDefaults implements config.Defaulter.
func (c *Config) SetDefaults() { c.UDP = DefaultAddr }

// Validate implements config.Validator.
func (c *Config) Validate() error {
	if err := ingestutil.CheckListen(c.UDP); err != nil {
		return fmt.Errorf("udp: %w", err)
	}
	_, err := parseNames(c.Names)
	return err
}

func parseNames(in map[string]string) (map[netip.Addr]string, error) {
	out := make(map[netip.Addr]string, len(in))
	for k, v := range in {
		ip, err := netip.ParseAddr(k)
		if err != nil {
			return nil, fmt.Errorf("names: %q is not an IP address", k)
		}
		if v == "" || ingestutil.StreamName(v) != v {
			return nil, fmt.Errorf("names: %q for %s must be non-empty, without spaces, control or glob characters", v, k)
		}
		out[ip.Unmap()] = v
	}
	return out, nil
}

// Register prepares the listener; start binds it and serves until its
// context ends.
func Register(_ *http.ServeMux, _ *mcpserver.Server, d deps.Deps, cfg *Config) (func(context.Context) error, error) {
	if cfg.UDP == "" {
		return nil, nil
	}
	s, err := New(d, cfg.Names)
	if err != nil {
		return nil, err
	}
	addr := cfg.UDP
	return func(ctx context.Context) error { return s.Run(ctx, addr) }, nil
}

// Server is the UDP listener and the per-source reassembly state. Only
// the reading goroutine touches that state.
type Server struct {
	d       deps.Deps
	names   map[netip.Addr]string
	log     *slog.Logger
	declare ingestutil.Declarer

	plain map[netip.Addr]*plainSource
	frags map[fragKey]*fragBuf

	// Bound, when set, is told the address once the listener is up.
	Bound func(net.Addr)
}

type plainSource struct {
	sp   *textclean.Splitter
	last time.Time
}

type fragKey struct {
	ip  netip.Addr
	seq uint64
}

type fragBuf struct {
	rec      Record // header of the first fragment seen
	body     []byte
	have     []bool
	received int
	started  time.Time
}

const (
	maxSources = 1024
	maxFrags   = 256
	// fragTimeout is how long a message with missing fragments waits
	// before what arrived is stored anyway.
	fragTimeout = 2 * time.Second
	tickEvery   = 250 * time.Millisecond
)

// New builds the server; names maps IP addresses to stream names.
func New(d deps.Deps, names map[string]string) (*Server, error) {
	n, err := parseNames(names)
	if err != nil {
		return nil, fmt.Errorf("netconsole.%w", err)
	}
	return &Server{
		d: d, names: n, log: d.Log.With("component", "netconsole"),
		declare: ingestutil.Declarer{Status: d.Status, Transport: "udp"},
		plain:   map[netip.Addr]*plainSource{},
		frags:   map[fragKey]*fragBuf{},
	}, nil
}

// Run binds addr and serves until ctx ends.
func (s *Server) Run(ctx context.Context, addr string) error {
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(ctx, "udp", addr)
	if err != nil {
		return fmt.Errorf("netconsole udp: %w", err)
	}
	s.log.Info("listening", "transport", "udp", "addr", pc.LocalAddr().String())
	if s.Bound != nil {
		s.Bound(pc.LocalAddr())
	}
	stop := context.AfterFunc(ctx, func() { pc.Close() })
	defer stop()
	defer pc.Close()
	buf := make([]byte, 64<<10)
	for {
		_ = pc.SetReadDeadline(time.Now().Add(tickEvery))
		n, from, err := pc.ReadFrom(buf)
		now := s.d.Clock.Now()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				s.tick(ctx, now)
				continue
			}
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				s.flushAll(ctx)
				return nil
			}
			s.log.Warn("udp read failed", "err", err)
			continue
		}
		s.Handle(ctx, buf[:n], ingestutil.PeerIP(from), now)
		s.tick(ctx, now)
	}
}

// Handle processes one datagram from ip received at now.
func (s *Server) Handle(ctx context.Context, b []byte, ip netip.Addr, now time.Time) {
	rec, ok := ParseExtended(b)
	if !ok {
		src := s.plain[ip]
		if src == nil {
			if len(s.plain) >= maxSources {
				s.flushPlain(ctx)
			}
			src = &plainSource{}
			src.sp = textclean.NewSplitter(textclean.DefaultFlushAfter, textclean.MaxLineBytes, func(line string, at time.Time) {
				s.submit(ctx, s.entry(ip, line, at, store.DefaultSeverity, nil))
			})
			s.plain[ip] = src
		}
		src.last = now
		src.sp.Write(b, now)
		return
	}
	if rec.Frag == nil {
		s.record(ctx, ip, rec, rec.Body, now)
		return
	}
	key := fragKey{ip, rec.Seq}
	fb := s.frags[key]
	if fb == nil {
		if len(s.frags) >= maxFrags {
			s.flushFrags(ctx, time.Time{})
		}
		fb = &fragBuf{rec: rec, body: make([]byte, rec.Frag.Total), have: make([]bool, rec.Frag.Total), started: now}
		s.frags[key] = fb
	}
	if rec.Frag.Total != len(fb.body) {
		return
	}
	for i, c := range rec.Body {
		j := rec.Frag.Offset + i
		if j >= len(fb.body) {
			break
		}
		if !fb.have[j] {
			fb.have[j] = true
			fb.received++
		}
		fb.body[j] = c
	}
	if fb.received == len(fb.body) {
		delete(s.frags, key)
		s.record(ctx, ip, fb.rec, fb.body, fb.started)
	}
}

// record stores one extended message, one entry per line of its text.
func (s *Server) record(ctx context.Context, ip netip.Addr, rec Record, body []byte, at time.Time) {
	text, dict := SplitBody(trimNewlines(body))
	var meta ingestutil.Meta
	meta.Add("seq", rec.Seq)
	meta.Add("ts_usec", rec.TSUsec)
	if rec.Flags != "-" {
		meta.Add("flags", rec.Flags)
	}
	if rec.Facility != 0 {
		meta.Add("facility", rec.Facility)
	}
	meta.Add("source_ip", ingestutil.AddrName(ip))
	if len(rec.Fields) > 0 {
		f := make(map[string]any, len(rec.Fields))
		for k, v := range rec.Fields {
			f[k] = v
		}
		meta.Add("fields", f)
	}
	if len(dict) > 0 {
		d := make(map[string]any, len(dict))
		for k, v := range dict {
			d[k] = v
		}
		meta.Add("dict", d)
	}
	raw := meta.JSON()
	lines := ingestutil.SplitLines(text)
	if len(lines) == 0 {
		lines = []string{""}
	}
	for _, line := range lines {
		s.submit(ctx, s.entry(ip, line, at, rec.Level, raw))
	}
}

func trimNewlines(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == 0) {
		b = b[:len(b)-1]
	}
	return b
}

// StreamName is the stream of a source: its configured name, else its IP.
func (s *Server) StreamName(ip netip.Addr) string {
	if n, ok := s.names[ip.Unmap()]; ok {
		return n
	}
	return ingestutil.AddrName(ip)
}

func (s *Server) entry(ip netip.Addr, line string, at time.Time, sev int, meta []byte) store.Entry {
	name := s.StreamName(ip)
	if meta == nil {
		var m ingestutil.Meta
		m.Add("source_ip", ingestutil.AddrName(ip))
		meta = m.JSON()
	}
	return store.Entry{
		TS: at, Stream: "netconsole:" + name, Kind: store.KindLine, Severity: sev,
		Host: name, App: "kernel", Text: line, Meta: meta,
	}
}

func (s *Server) submit(ctx context.Context, e store.Entry) {
	s.declare.Declare(e.Stream)
	if err := s.d.Writer.Submit(ctx, e, true); err != nil {
		if errors.Is(err, writer.ErrFull) {
			s.d.Status.Dropped(e.Stream, 1)
		} else if !errors.Is(err, writer.ErrClosed) {
			s.log.Debug("netconsole entry refused", "stream", e.Stream, "err", err)
		}
	}
}

// tick flushes partial plain lines and stale fragments.
func (s *Server) tick(ctx context.Context, now time.Time) {
	for ip, src := range s.plain {
		src.sp.Tick(now)
		if now.Sub(src.last) > time.Minute {
			src.sp.Close()
			delete(s.plain, ip)
		}
	}
	s.flushFrags(ctx, now.Add(-fragTimeout))
}

// flushFrags stores incomplete messages started before cutoff (all of
// them for a zero cutoff), with what arrived.
func (s *Server) flushFrags(ctx context.Context, cutoff time.Time) {
	for k, fb := range s.frags {
		if cutoff.IsZero() || fb.started.Before(cutoff) {
			delete(s.frags, k)
			var got []byte
			for i, c := range fb.body {
				if fb.have[i] {
					got = append(got, c)
				}
			}
			s.record(ctx, k.ip, fb.rec, got, fb.started)
		}
	}
}

func (s *Server) flushPlain(_ context.Context) {
	for ip, src := range s.plain {
		src.sp.Close()
		delete(s.plain, ip)
	}
}

func (s *Server) flushAll(ctx context.Context) {
	s.flushPlain(ctx)
	s.flushFrags(ctx, time.Time{})
}
