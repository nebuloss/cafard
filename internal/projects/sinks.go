package projects

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// Sink is a named log source of a project. Its streams are
// <kind>:<project>/<name>, one per transport.
type Sink struct {
	ID        int64
	Project   string
	Name      string
	Title     string
	CreatedAt time.Time
	// Sources are the syslog and netconsole senders that land here.
	Sources []netip.Prefix
	// Serial is the sink's port, nil when it has none. Its Name is Ref().
	Serial *SerialPort
}

// Ref is "project/name", the part of its stream ids after the kind.
func (k Sink) Ref() string { return k.Project + "/" + k.Name }

// Stream is the sink's stream of one transport kind: "syslog", "push", ...
func (k Sink) Stream(kind string) string { return kind + ":" + k.Ref() }

// SplitRef reads "project/name".
func SplitRef(ref string) (project, name string, ok bool) {
	project, name, ok = strings.Cut(ref, "/")
	return project, name, ok && project != "" && name != "" && !strings.Contains(name, "/")
}

// ParseSources reads IPs and CIDRs separated by spaces, commas or lines.
func ParseSources(text string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		ip, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or a CIDR such as 10.0.50.0/24", f)
		}
		ip = ip.Unmap().WithZone("")
		out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int { return strings.Compare(a.String(), b.String()) })
	return slices.Compact(out), nil
}

// Sinks lists the sinks of a project, or of every project for "".
func (s *Service) Sinks(ctx context.Context, project string) ([]Sink, error) {
	return s.querySinks(ctx, `WHERE ? = '' OR s.project = ?`, project, project)
}

// Sink looks one up.
func (s *Service) Sink(ctx context.Context, project, name string) (Sink, error) {
	ks, err := s.querySinks(ctx, `WHERE s.project = ? AND s.name = ?`, project, name)
	if err != nil {
		return Sink{}, err
	}
	if len(ks) == 0 {
		return Sink{}, fmt.Errorf("%w: sink %s/%s", ErrNotFound, project, name)
	}
	return ks[0], nil
}

func (s *Service) querySinks(ctx context.Context, where string, args ...any) ([]Sink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.project, s.name, s.title, s.created_at,
		p.sink IS NOT NULL, coalesce(p.device, ''), coalesce(p.rfc2217, ''), coalesce(p.tcp, ''),
		coalesce(p.baud, 0), coalesce(p.data_bits, 0), coalesce(p.parity, ''), coalesce(p.stop_bits, 0), coalesce(p.writable, 0)
		FROM sinks s LEFT JOIN sink_serial p ON p.sink = s.id `+where+` ORDER BY s.project, s.name`, args...)
	if err != nil {
		return nil, err
	}
	var out []Sink
	for rows.Next() {
		var (
			k         Sink
			created   int64
			hasSerial bool
			port      SerialPort
		)
		if err := rows.Scan(&k.ID, &k.Project, &k.Name, &k.Title, &created, &hasSerial, &port.Device, &port.RFC2217, &port.TCP,
			&port.Baud, &port.DataBits, &port.Parity, &port.StopBits, &port.Writable); err != nil {
			rows.Close()
			return nil, err
		}
		k.CreatedAt = time.Unix(0, created).UTC()
		if hasSerial {
			port.Name = k.Ref()
			k.Serial = &port
		}
		out = append(out, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sources, err := s.sources(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Sources = sources[out[i].ID]
	}
	return out, nil
}

func (s *Service) sources(ctx context.Context) (map[int64][]netip.Prefix, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sink, prefix FROM sink_sources ORDER BY sink, prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]netip.Prefix{}
	for rows.Next() {
		var (
			id  int64
			raw string
		)
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		if p, err := netip.ParsePrefix(raw); err == nil {
			out[id] = append(out[id], p)
		}
	}
	return out, rows.Err()
}

// SaveSink makes a sink, or replaces the title, sources and port of the
// one with its project and name.
func (s *Service) SaveSink(ctx context.Context, k Sink) (Sink, error) {
	if _, err := s.Project(ctx, k.Project); err != nil {
		return Sink{}, err
	}
	if err := auth.ValidateName(k.Name); err != nil {
		return Sink{}, dto.Errorf(dto.CodeBadRequest, "A sink name must be 1 to 40 characters of a-z, 0-9, '.', '_' or '-', starting with a letter or digit, not %q.", k.Name)
	}
	k.Title = strings.TrimSpace(k.Title)
	if len(k.Title) > maxTitle {
		return Sink{}, dto.Errorf(dto.CodeBadRequest, "A sink title is at most %d bytes.", maxTitle)
	}
	if k.Serial != nil {
		port := *k.Serial
		port.Name = k.Ref()
		port.SetDefaults()
		if err := port.Validate(); err != nil {
			return Sink{}, dto.Errorf(dto.CodeBadRequest, "The serial port of %v.", err)
		}
		k.Serial = &port
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sink{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sinks(project, name, title, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(project, name) DO UPDATE SET title = excluded.title`, k.Project, k.Name, k.Title, s.clock.Now().UnixNano()); err != nil {
		return Sink{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sinks WHERE project = ? AND name = ?`, k.Project, k.Name).Scan(&k.ID); err != nil {
		return Sink{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sink_sources WHERE sink = ?`, k.ID); err != nil {
		return Sink{}, err
	}
	for _, p := range k.Sources {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sink_sources(sink, prefix) VALUES (?, ?) ON CONFLICT DO NOTHING`, k.ID, p.Masked().String()); err != nil {
			return Sink{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sink_serial WHERE sink = ?`, k.ID); err != nil {
		return Sink{}, err
	}
	if p := k.Serial; p != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sink_serial(sink, device, rfc2217, tcp, baud, data_bits, parity, stop_bits, writable)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, k.ID, nullText(p.Device), nullText(p.RFC2217), nullText(p.TCP),
			nullZero(p.Baud), nullZero(p.DataBits), nullText(p.Parity), nullZero(p.StopBits), p.Writable); err != nil {
			return Sink{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Sink{}, err
	}
	if err := s.sinksChanged(ctx); err != nil {
		return Sink{}, err
	}
	return s.Sink(ctx, k.Project, k.Name)
}

func nullText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullZero[T int | float64](v T) any {
	if v == 0 {
		return nil
	}
	return v
}

// DeleteSink removes a sink, its sources, its port and its tokens. Its
// streams keep their history, which ages out by the project's retention.
func (s *Service) DeleteSink(ctx context.Context, project, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sinks WHERE project = ? AND name = ?`, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: sink %s/%s", ErrNotFound, project, name)
	}
	return s.sinksChanged(ctx)
}

// OnSinksChanged runs f after a sink is made, changed or deleted (the
// serial manager reconciles its ports). Call it before serving.
func (s *Service) OnSinksChanged(f func()) {
	s.mu.Lock()
	s.onSinks = append(s.onSinks, f)
	s.mu.Unlock()
}

func (s *Service) sinksChanged(ctx context.Context) error {
	if err := s.reload(ctx); err != nil {
		return err
	}
	s.Invalidate()
	s.mu.Lock()
	hooks := append([]func(){}, s.onSinks...)
	s.mu.Unlock()
	for _, f := range hooks {
		f()
	}
	return nil
}

// routes is the in-memory copy of the sinks that ingest asks on every
// datagram, swapped whole on change.
type routes struct {
	sinks    []Sink // by id
	byRef    map[string]Sink
	byName   map[string][]Sink
	prefixes []route // most specific first, then oldest sink
}

type route struct {
	prefix netip.Prefix
	sink   int // index in sinks
}

func (s *Service) reload(ctx context.Context) error {
	ks, err := s.Sinks(ctx, "")
	if err != nil {
		return err
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].ID < ks[j].ID })
	r := &routes{sinks: ks, byRef: map[string]Sink{}, byName: map[string][]Sink{}}
	for i, k := range ks {
		r.byRef[k.Ref()] = k
		r.byName[k.Name] = append(r.byName[k.Name], k)
		for _, p := range k.Sources {
			r.prefixes = append(r.prefixes, route{prefix: p, sink: i})
		}
	}
	sort.SliceStable(r.prefixes, func(i, j int) bool { return r.prefixes[i].prefix.Bits() > r.prefixes[j].prefix.Bits() })
	s.routes.Store(r)
	return nil
}

// SinkForAddr is the sink a syslog or netconsole sender lands in: the most
// specific prefix holding ip, then the oldest sink.
func (s *Service) SinkForAddr(ip netip.Addr) (Sink, bool) {
	ip = ip.Unmap().WithZone("")
	r := s.routes.Load()
	for _, rt := range r.prefixes {
		if rt.prefix.Contains(ip) {
			return r.sinks[rt.sink], true
		}
	}
	return Sink{}, false
}

// SinkByRef is a sink by "project/name", from memory.
func (s *Service) SinkByRef(ref string) (Sink, bool) {
	k, ok := s.routes.Load().byRef[ref]
	return k, ok
}

// SinkForIngest is where a token's logs go: its sink for a sink token;
// else the sink name names (a kind prefix is ignored), as "project/sink"
// or a bare sink only one of the projects p may ingest into has.
func (s *Service) SinkForIngest(ctx context.Context, p auth.Principal, name string) (Sink, bool) {
	if !p.Has(auth.ScopeIngest) {
		return Sink{}, false
	}
	if p.Sink != "" {
		return s.SinkByRef(p.Sink)
	}
	if _, rest, ok := strings.Cut(name, ":"); ok {
		name = rest
	}
	a := s.Access(ctx, p)
	if project, sink, ok := SplitRef(name); ok {
		k, found := s.SinkByRef(project + "/" + sink)
		return k, found && a.ProjectScopes(project).Has(auth.ScopeIngest)
	}
	var found []Sink
	for _, k := range s.routes.Load().byName[name] {
		if a.ProjectScopes(k.Project).Has(auth.ScopeIngest) {
			found = append(found, k)
		}
	}
	if len(found) != 1 {
		return Sink{}, false
	}
	return found[0], true
}

// SerialPorts are the ports of every sink that has one.
func (s *Service) SerialPorts() []SerialPort {
	var out []SerialPort
	for _, k := range s.routes.Load().sinks {
		if k.Serial != nil {
			out = append(out, *k.Serial)
		}
	}
	return out
}
