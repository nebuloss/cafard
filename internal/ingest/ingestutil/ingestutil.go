// Package ingestutil holds what the network ingesters (syslog, netconsole,
// OTLP) share: stream names from untrusted input, capped meta JSON, source
// allowlists, and declaring sources once.
package ingestutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/status"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
	"github.com/nebuloss/cafard/internal/writer"
)

// maxNameBytes keeps kind:name under the 200 bytes store.ValidateStreamID allows.
const maxNameBytes = 160

// StreamName makes a stream name out of untrusted text (a hostname, a
// service name): characters a stream id refuses become '_', and it is
// cut to a length every kind prefix fits with. "" stays "".
func StreamName(s string) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "_")
	var b strings.Builder
	for _, r := range s {
		if b.Len()+utf8.RuneLen(r) > maxNameBytes {
			break
		}
		if r <= ' ' || r == 0x7f || r == '*' || r == '?' || r == '[' || r == '\\' || r == utf8.RuneError {
			r = '_'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// AddrName is the stream name of a peer address: its IP, IPv4-mapped
// IPv6 addresses unmapped, without the port or zone.
func AddrName(ip netip.Addr) string {
	return ip.Unmap().WithZone("").String()
}

// PeerIP is the IP of a net.Addr from a UDP or TCP socket.
func PeerIP(a net.Addr) netip.Addr {
	switch v := a.(type) {
	case *net.UDPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	case *net.TCPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

// SplitLines normalises text the way every ingester does (UTF-8 repaired,
// \n, \r\n and lone \r end lines, long lines split). A trailing newline
// does not make an empty last line. Empty text is one empty line, so an
// empty message still makes an entry.
func SplitLines(b []byte) []string {
	var lines []string
	sp := textclean.NewSplitter(time.Hour, textclean.MaxLineBytes, func(line string, _ time.Time) {
		lines = append(lines, line)
	})
	sp.Write(b, time.Time{})
	sp.Close()
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// AnyMap copies m into the map[string]any Meta.Add expects for objects.
func AnyMap[V any](m map[string]V) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// SubmitLossy queues e without waiting: a full queue counts a drop on
// the stream, and other refusals (bar shutdown) are logged at debug.
func SubmitLossy(ctx context.Context, d deps.Deps, log *slog.Logger, e store.Entry) {
	if err := d.Writer.Submit(ctx, e, true); err != nil {
		if errors.Is(err, writer.ErrFull) {
			d.Status.Dropped(e.Stream, 1)
		} else if !errors.Is(err, writer.ErrClosed) {
			log.Debug("entry refused", "stream", e.Stream, "err", err)
		}
	}
}

// unroutedLogEvery bounds the debug lines about logs no sink wants: a
// stray sender on the LAN must not fill the journal.
const unroutedLogEvery = time.Minute

// Unrouted drops the logs of one transport that reached no sink: it
// counts them in the status, and says so at debug level at most once a
// minute, with how many went since.
type Unrouted struct {
	Status    status.Registry
	Clock     clock.Clock
	Log       *slog.Logger
	Transport string

	mu     sync.Mutex
	last   time.Time
	missed int
}

// Drop counts n entries; args describe the last of them for the log line.
func (u *Unrouted) Drop(n int, args ...any) {
	if n <= 0 {
		return
	}
	u.Status.Unrouted(u.Transport, n)
	now := u.Clock.Now()
	u.mu.Lock()
	u.missed += n
	if !u.last.IsZero() && now.Sub(u.last) < unroutedLogEvery {
		u.mu.Unlock()
		return
	}
	missed := u.missed
	u.last, u.missed = now, 0
	u.mu.Unlock()
	u.Log.Debug("logs reached no sink and were dropped", append([]any{"transport", u.Transport, "entries", missed}, args...)...)
}

// Allowlist is a set of source prefixes. An empty list allows everyone.
type Allowlist []netip.Prefix

// ParseAllowlist reads CIDRs ("10.0.50.0/24") or bare addresses.
func ParseAllowlist(items []string) (Allowlist, error) {
	var out Allowlist
	for _, s := range items {
		s = strings.TrimSpace(s)
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("%q is neither a CIDR like 10.0.50.0/24 nor an IP address", s)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// Allows reports whether ip may send.
func (l Allowlist) Allows(ip netip.Addr) bool {
	if len(l) == 0 {
		return true
	}
	ip = ip.Unmap()
	for _, p := range l {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// CheckListen checks a listen address; "" (disabled) is valid.
func CheckListen(addr string) error {
	if addr == "" {
		return nil
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("%q is not host:port (or empty to disable): %v", addr, err)
	}
	return nil
}

// maxDeclared bounds the set of streams remembered as declared. Anyone on
// the LAN can invent hostnames, so the set is cleared rather than grown.
const maxDeclared = 4096

// Declarer declares each source in the status registry once.
type Declarer struct {
	Status    status.Registry
	Transport string

	mu   sync.Mutex
	seen map[string]bool
}

// Declare declares stream if it was not declared before.
func (d *Declarer) Declare(stream string) {
	d.mu.Lock()
	if d.seen[stream] {
		d.mu.Unlock()
		return
	}
	if d.seen == nil || len(d.seen) >= maxDeclared {
		d.seen = map[string]bool{}
	}
	d.seen[stream] = true
	d.mu.Unlock()
	d.Status.Declare(status.Source{Stream: stream, Transport: d.Transport})
}

// Meta builds an entry's meta JSON and caps it at store.MaxMetaBytes:
// scalar fields come first, then object fields keep the keys that fit,
// and "truncated": true says something was lost.
type Meta struct {
	fields []metaField
}

type metaField struct {
	key string
	val any
	obj map[string]any // set for object fields, which may be cut
}

// Add appends a field; empty strings, nil and empty maps are skipped.
func (m *Meta) Add(key string, v any) {
	switch x := v.(type) {
	case nil:
		return
	case string:
		if x == "" {
			return
		}
	case map[string]any:
		if len(x) == 0 {
			return
		}
		m.fields = append(m.fields, metaField{key: key, obj: x})
		return
	}
	m.fields = append(m.fields, metaField{key: key, val: v})
}

const truncatedField = `"truncated":true`

// JSON renders the fields, or nil when there are none.
func (m *Meta) JSON() json.RawMessage {
	return m.render(store.MaxMetaBytes)
}

func (m *Meta) render(max int) json.RawMessage {
	if len(m.fields) == 0 {
		return nil
	}
	budget := max - len(truncatedField) - 3 // braces and a comma
	var buf bytes.Buffer
	buf.WriteByte('{')
	truncated := false
	add := func(part []byte) bool {
		extra := len(part)
		if buf.Len() > 1 {
			extra++
		}
		if buf.Len()+extra > budget {
			return false
		}
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		buf.Write(part)
		return true
	}
	// Scalars first: they are small and say the most (ids, facility).
	// Objects (attributes) then share what is left, in the order added.
	for _, f := range m.fields {
		if f.obj != nil {
			continue
		}
		key, _ := json.Marshal(f.key)
		val, err := json.Marshal(f.val)
		if err != nil || !add(append(append(key, ':'), val...)) {
			truncated = true
		}
	}
	for _, f := range m.fields {
		if f.obj == nil {
			continue
		}
		key, _ := json.Marshal(f.key)
		obj, cut := fitObject(f.obj, budget-buf.Len()-len(key)-2)
		truncated = truncated || cut
		if obj == nil || !add(append(append(key, ':'), obj...)) {
			truncated = true
		}
	}
	if truncated {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		buf.WriteString(truncatedField)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// fitObject renders as many keys of obj (sorted) as fit in budget bytes.
func fitObject(obj map[string]any, budget int) ([]byte, bool) {
	if budget < 2 {
		return nil, true
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte('{')
	cut := false
	for _, k := range keys {
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(obj[k])
		if err != nil {
			cut = true
			continue
		}
		extra := len(kb) + 1 + len(vb)
		if buf.Len() > 1 {
			extra++
		}
		if buf.Len()+extra+1 > budget {
			cut = true
			continue
		}
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	if buf.Len() == 1 {
		return nil, true
	}
	buf.WriteByte('}')
	return buf.Bytes(), cut
}
