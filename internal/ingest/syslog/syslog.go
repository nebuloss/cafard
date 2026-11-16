// Package syslog receives syslog over UDP and TCP: RFC 5424 and RFC 3164
// messages, best effort, with RFC 6587 framing on TCP.
package syslog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/ingest/ingestutil"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/writer"
)

// DefaultAddr is where both listeners bind by default. Port 514 needs
// privileges, so it is opt-in.
const DefaultAddr = ":5514"

// MaxConns bounds concurrent TCP connections.
const MaxConns = 512

// Config is the `syslog:` config section.
type Config struct {
	// UDP and TCP are listen addresses; empty disables that listener.
	UDP string `yaml:"udp"`
	TCP string `yaml:"tcp"`
	// Allow lists the source CIDRs (or addresses) accepted; empty accepts
	// everyone.
	Allow []string `yaml:"allow,omitempty"`
}

// SetDefaults implements config.Defaulter.
func (c *Config) SetDefaults() {
	c.UDP, c.TCP = DefaultAddr, DefaultAddr
}

// Validate implements config.Validator.
func (c *Config) Validate() error {
	if err := ingestutil.CheckListen(c.UDP); err != nil {
		return fmt.Errorf("udp: %w", err)
	}
	if err := ingestutil.CheckListen(c.TCP); err != nil {
		return fmt.Errorf("tcp: %w", err)
	}
	if _, err := ingestutil.ParseAllowlist(c.Allow); err != nil {
		return fmt.Errorf("allow: %w", err)
	}
	return nil
}

// Register prepares the listeners; start binds them and serves until its
// context ends.
func Register(_ *http.ServeMux, _ *mcpserver.Server, d deps.Deps, cfg *Config) (func(context.Context) error, error) {
	if cfg.UDP == "" && cfg.TCP == "" {
		return nil, nil
	}
	s, err := New(d, cfg.Allow)
	if err != nil {
		return nil, err
	}
	udpAddr, tcpAddr := cfg.UDP, cfg.TCP
	return func(ctx context.Context) error { return s.Run(ctx, udpAddr, tcpAddr) }, nil
}

// New builds the server; allow is the source allowlist.
func New(d deps.Deps, allow []string) (*Server, error) {
	al, err := ingestutil.ParseAllowlist(allow)
	if err != nil {
		return nil, fmt.Errorf("syslog.allow: %w", err)
	}
	return &Server{
		d:     d,
		allow: al,
		log:   d.Log.With("component", "syslog"),
		udp:   ingestutil.Declarer{Status: d.Status, Transport: "udp"},
		tcp:   ingestutil.Declarer{Status: d.Status, Transport: "tcp"},
		peers: map[string]int{},
	}, nil
}

// Server is the two listeners.
type Server struct {
	d     deps.Deps
	allow ingestutil.Allowlist
	log   *slog.Logger
	udp   ingestutil.Declarer
	tcp   ingestutil.Declarer

	mu    sync.Mutex
	peers map[string]int // TCP connections per stream, for SetConnected

	// Bound, when set, is told the addresses once both listeners are up.
	Bound func(udp, tcp net.Addr)
}

// Run binds and serves until ctx ends. An empty address skips a listener.
func (s *Server) Run(ctx context.Context, udpAddr, tcpAddr string) error {
	var (
		pc net.PacketConn
		ln net.Listener
		lc net.ListenConfig
	)
	if udpAddr != "" {
		var err error
		if pc, err = lc.ListenPacket(ctx, "udp", udpAddr); err != nil {
			return fmt.Errorf("syslog udp: %w", err)
		}
	}
	if tcpAddr != "" {
		var err error
		if ln, err = lc.Listen(ctx, "tcp", tcpAddr); err != nil {
			if pc != nil {
				pc.Close()
			}
			return fmt.Errorf("syslog tcp: %w", err)
		}
	}
	var ua, ta net.Addr
	if pc != nil {
		ua = pc.LocalAddr()
		s.log.Info("listening", "transport", "udp", "addr", ua.String())
	}
	if ln != nil {
		ta = ln.Addr()
		s.log.Info("listening", "transport", "tcp", "addr", ta.String())
	}
	if s.Bound != nil {
		s.Bound(ua, ta)
	}
	var wg sync.WaitGroup
	if pc != nil {
		wg.Go(func() { s.serveUDP(ctx, pc) })
	}
	if ln != nil {
		wg.Go(func() { s.serveTCP(ctx, ln) })
	}
	<-ctx.Done()
	if pc != nil {
		pc.Close()
	}
	if ln != nil {
		ln.Close()
	}
	wg.Wait()
	return nil
}

// entries turns one message from ip into entries, one per line of its text.
func (s *Server) entries(b []byte, ip netip.Addr, at time.Time) []store.Entry {
	m := Parse(b, s.d.Clock.Now(), time.Local)
	return MessageEntries(m, ip, at)
}

// MessageEntries maps a parsed message to entries: stream
// syslog:<hostname, else source IP>, one entry per line of its text.
func MessageEntries(m Message, ip netip.Addr, at time.Time) []store.Entry {
	name := ingestutil.StreamName(m.Hostname)
	host := name
	if name == "" {
		name = ingestutil.AddrName(ip)
		host = name
	}
	var meta ingestutil.Meta
	if m.Facility >= 0 {
		meta.Add("facility", m.Facility)
		meta.Add("facility_name", FacilityName(m.Facility))
	}
	meta.Add("format", string(m.Format))
	meta.Add("source_ip", ingestutil.AddrName(ip))
	meta.Add("proc_id", m.ProcID)
	meta.Add("msg_id", m.MsgID)
	if len(m.SD) > 0 {
		sd := make(map[string]any, len(m.SD))
		for id, params := range m.SD {
			sd[id] = params
		}
		meta.Add("sd", sd)
	}
	raw := meta.JSON()
	lines := ingestutil.SplitLines(m.Text)
	if len(lines) == 0 {
		lines = []string{""}
	}
	out := make([]store.Entry, len(lines))
	for i, line := range lines {
		out[i] = store.Entry{
			TS:       at,
			SrcTS:    m.Time,
			Stream:   "syslog:" + name,
			Kind:     store.KindLine,
			Severity: m.Severity,
			Host:     host,
			App:      ingestutil.StreamName(m.AppName),
			Text:     line,
			Meta:     raw,
		}
	}
	return out
}

func (s *Server) serveUDP(ctx context.Context, pc net.PacketConn) {
	buf := make([]byte, 64<<10)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Warn("udp read failed", "err", err)
			continue
		}
		ip := ingestutil.PeerIP(addr)
		if !s.allow.Allows(ip) {
			continue
		}
		at := s.d.Clock.Now()
		for _, e := range s.entries(trimTrailing(buf[:n]), ip, at) {
			s.udp.Declare(e.Stream)
			if err := s.d.Writer.Submit(ctx, e, true); err != nil {
				if errors.Is(err, writer.ErrFull) {
					s.d.Status.Dropped(e.Stream, 1)
				} else if !errors.Is(err, writer.ErrClosed) {
					s.log.Debug("syslog entry refused", "stream", e.Stream, "err", err)
				}
			}
		}
	}
}

// trimTrailing drops the newlines and NULs senders append to datagrams.
func trimTrailing(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == 0) {
		b = b[:len(b)-1]
	}
	return b
}

func (s *Server) serveTCP(ctx context.Context, ln net.Listener) {
	var wg sync.WaitGroup
	defer wg.Wait()
	sem := make(chan struct{}, MaxConns)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Warn("tcp accept failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		ip := ingestutil.PeerIP(c.RemoteAddr())
		if !s.allow.Allows(ip) {
			c.Close()
			continue
		}
		select {
		case sem <- struct{}{}:
		default:
			s.log.Warn("too many syslog connections, refusing one", "peer", ip.String())
			c.Close()
			continue
		}
		wg.Go(func() {
			defer func() { <-sem }()
			s.handleConn(ctx, c, ip)
		})
	}
}

// tickEvery is how often a quiet connection checks for a partial line.
const tickEvery = 250 * time.Millisecond

func (s *Server) handleConn(ctx context.Context, c net.Conn, ip netip.Addr) {
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	defer c.Close()

	streams := map[string]bool{}
	reason := "connection closed"
	defer func() {
		for st := range streams {
			s.release(st, reason)
		}
	}()
	f := newFramer(func(msg []byte, at time.Time) {
		for _, e := range s.entries(msg, ip, at) {
			if !streams[e.Stream] {
				streams[e.Stream] = true
				s.tcp.Declare(e.Stream)
				s.acquire(e.Stream)
			}
			if err := s.d.Writer.Submit(ctx, e, false); err != nil && ctx.Err() == nil && !errors.Is(err, writer.ErrClosed) {
				s.log.Debug("syslog entry refused", "stream", e.Stream, "err", err)
			}
		}
	})
	buf := make([]byte, 32<<10)
	for {
		_ = c.SetReadDeadline(time.Now().Add(tickEvery))
		n, err := c.Read(buf)
		now := s.d.Clock.Now()
		if n > 0 {
			f.Write(buf[:n], now)
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				f.Tick(now)
				continue
			}
			if ctx.Err() != nil {
				reason = "server stopping"
			} else if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				reason = "connection lost: " + err.Error()
			}
			f.Close()
			return
		}
	}
}

func (s *Server) acquire(stream string) {
	s.mu.Lock()
	s.peers[stream]++
	s.mu.Unlock()
	s.d.Status.SetConnected(stream, true, "")
}

// release marks a stream down once its last TCP connection is gone.
func (s *Server) release(stream, reason string) {
	s.mu.Lock()
	s.peers[stream]--
	last := s.peers[stream] <= 0
	if last {
		delete(s.peers, stream)
	}
	s.mu.Unlock()
	if last {
		s.d.Status.SetConnected(stream, false, reason)
	}
}
