package messaging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// General is the channel that always exists.
const General = "general"

// systemName is created_by for channels cafard makes itself.
const systemName = "cafard"

const (
	maxTitle = 200
	maxTopic = 1000
)

var channelNameRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// ValidateChannelName checks a new channel name: a lowercase slug of 1 to
// 40 characters, not a reserved name.
func ValidateChannelName(name string) error {
	if !channelNameRE.MatchString(name) {
		return fmt.Errorf("channel name %q must be 1 to 40 characters of a-z, 0-9 and '-'", name)
	}
	if name == "dm" || name == General {
		return fmt.Errorf("channel name %q is reserved", name)
	}
	return nil
}

// Channel is one row of channels with its bindings.
type Channel struct {
	Name         string
	Title, Topic string
	Surface      dto.Surface
	CreatedBy    string
	CreatedAt    time.Time
	LastActivity time.Time
	ArchivedAt   *time.Time
	Source       dto.ChatSource
	Patterns     []string
}

// Stream is the channel's message stream.
func (c Channel) Stream() string { return "chan:" + c.Name }

// Archived reports whether the channel is archived.
func (c Channel) Archived() bool { return c.ArchivedAt != nil }

// CanManage reports whether p may rename, archive or delete c.
func (c Channel) CanManage(p auth.Principal) bool {
	return c.Source == dto.SourceRuntime && (p.Name == c.CreatedBy || p.Has(auth.ScopeAdmin)) && p.Has(auth.ScopeMessage)
}

// normalizeChannel accepts "dut1", "#dut1" and "chan:dut1".
func normalizeChannel(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "#")
	return strings.TrimPrefix(name, "chan:")
}

var errNoChannel = errors.New("no such channel")

func (s *Service) channel(ctx context.Context, name string) (Channel, error) {
	chs, err := s.queryChannels(ctx, `WHERE name = ?`, normalizeChannel(name))
	if err != nil {
		return Channel{}, err
	}
	if len(chs) == 0 {
		return Channel{}, errNoChannel
	}
	return chs[0], nil
}

// Channel returns a channel, or an unknown_channel error listing the
// existing ones.
func (s *Service) Channel(ctx context.Context, name string) (Channel, error) {
	ch, err := s.channel(ctx, name)
	if errors.Is(err, errNoChannel) {
		return Channel{}, s.unknownChannel(ctx, name)
	}
	return ch, err
}

// Channels lists channels by name.
func (s *Service) Channels(ctx context.Context, includeArchived bool) ([]Channel, error) {
	if includeArchived {
		return s.queryChannels(ctx, ``)
	}
	return s.queryChannels(ctx, `WHERE archived_at IS NULL`)
}

func (s *Service) queryChannels(ctx context.Context, where string, args ...any) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, coalesce(title, ''), coalesce(topic, ''), surface, created_by, created_at,
		last_activity, archived_at, source FROM channels `+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	var out []Channel
	for rows.Next() {
		var (
			c                   Channel
			created, activity   int64
			archived            sql.NullInt64
			surface, sourceName string
		)
		if err := rows.Scan(&c.Name, &c.Title, &c.Topic, &surface, &c.CreatedBy, &created, &activity, &archived, &sourceName); err != nil {
			rows.Close()
			return nil, err
		}
		c.Surface, c.Source = dto.Surface(surface), dto.ChatSource(sourceName)
		c.CreatedAt, c.LastActivity = time.Unix(0, created).UTC(), time.Unix(0, activity).UTC()
		if archived.Valid {
			t := time.Unix(0, archived.Int64).UTC()
			c.ArchivedAt = &t
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Patterns, err = s.patterns(ctx, out[i].Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) patterns(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pattern FROM channel_streams WHERE channel = ? ORDER BY pattern`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) unknownChannel(ctx context.Context, name string) *dto.APIError {
	chs, err := s.queryChannels(ctx, ``)
	var names []string
	if err == nil {
		for _, c := range chs {
			names = append(names, c.Name)
		}
	}
	return dto.Errorf(dto.CodeUnknownChannel, "There is no channel %q. Existing channels: %s.", normalizeChannel(name), strings.Join(names, ", "))
}

// ResolveChannel returns the channel's binding patterns: stream ids and
// globs, which the log tools and the hub match themselves, so a stream
// that appears during a wait_for is still seen. An agent that reads a
// channel this way follows it.
func (s *Service) ResolveChannel(ctx context.Context, name string) ([]string, error) {
	ch, err := s.Channel(ctx, name)
	if err != nil {
		return nil, err
	}
	if p, ok := auth.PrincipalFrom(ctx); ok && p.Kind == auth.KindAgent {
		if err := s.autoFollow(ctx, p.Name, ch.Stream()); err != nil {
			return nil, err
		}
	}
	return slices.Clone(ch.Patterns), nil
}

// BoundStreams are the existing log streams a channel's bindings match.
func (s *Service) BoundStreams(ctx context.Context, ch Channel) ([]string, error) {
	if len(ch.Patterns) == 0 {
		return []string{}, nil
	}
	ids, err := s.st.MatchStreams(ctx, ch.Patterns)
	if err != nil {
		return nil, err
	}
	ids = slices.DeleteFunc(ids, store.IsMessageStream)
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// applyConfigChannels makes sure general exists and reapplies the
// `channels:` list. A config channel that left the list becomes a
// runtime one, which its creator may then delete.
func (s *Service) applyConfigChannels(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.clock.Now().UnixNano()
	if _, err := tx.ExecContext(ctx, `INSERT INTO channels(name, surface, created_by, created_at, last_activity, source)
		VALUES (?, 'markers', ?, ?, ?, 'config') ON CONFLICT(name) DO NOTHING`, General, systemName, now, now); err != nil {
		return err
	}
	listed := map[string]bool{General: true}
	for _, c := range s.opts.Channels.List {
		listed[c.Name] = true
		if c.Name == General {
			if _, err := tx.ExecContext(ctx, `UPDATE channels SET title = ?, topic = ? WHERE name = ?`,
				nullStr(c.Title), nullStr(c.Topic), General); err != nil {
				return err
			}
			continue
		}
		surface := c.Surface
		if surface == "" {
			surface = dto.SurfaceMarkers
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO channels(name, title, topic, surface, created_by, created_at, last_activity, source)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'config')
			ON CONFLICT(name) DO UPDATE SET title = excluded.title, topic = excluded.topic, surface = excluded.surface,
			source = 'config', archived_at = NULL`,
			c.Name, nullStr(c.Title), nullStr(c.Topic), string(surface), systemName, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM channel_streams WHERE channel = ?`, c.Name); err != nil {
			return err
		}
		for _, p := range c.Streams {
			if _, err := tx.ExecContext(ctx, `INSERT INTO channel_streams(channel, pattern) VALUES (?, ?) ON CONFLICT DO NOTHING`, c.Name, p); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT name FROM channels WHERE source = 'config'`)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		if !listed[n] {
			stale = append(stale, n)
		}
	}
	rows.Close()
	for _, n := range stale {
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET source = 'runtime' WHERE name = ?`, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// nullStr is NULL for an empty text column.
var nullStr = store.NullString

func checkTexts(title, topic *string) error {
	if title != nil && len(*title) > maxTitle {
		return dto.Errorf(dto.CodeTooLarge, "A channel title is at most %d bytes.", maxTitle)
	}
	if topic != nil && len(*topic) > maxTopic {
		return dto.Errorf(dto.CodeTooLarge, "A channel topic is at most %d bytes.", maxTopic)
	}
	return nil
}

func checkPatterns(ps []string) error {
	for _, p := range ps {
		if err := validateBinding(p); err != nil {
			return dto.Errorf(dto.CodeInvalidPattern, "%v.", err)
		}
	}
	return nil
}

func needMessage(p auth.Principal) error {
	if !p.Has(auth.ScopeMessage) {
		return auth.MissingScope(auth.ScopeMessage)
	}
	return nil
}

// CreateChannel creates a runtime channel; its creator follows it.
func (s *Service) CreateChannel(ctx context.Context, p auth.Principal, req dto.ChannelCreate) (Channel, error) {
	if err := needMessage(p); err != nil {
		return Channel{}, err
	}
	name := normalizeChannel(req.Name)
	if err := ValidateChannelName(name); err != nil {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "%v.", err)
	}
	if err := checkTexts(&req.Title, &req.Topic); err != nil {
		return Channel{}, err
	}
	if err := checkPatterns(req.Patterns); err != nil {
		return Channel{}, err
	}
	surface := req.Surface
	if surface == "" {
		surface = dto.SurfaceMarkers
	}
	if !validSurface(surface) {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "surface %q is not none, markers or errors.", surface)
	}
	if _, err := s.channel(ctx, name); err == nil {
		return Channel{}, dto.Errorf(dto.CodeConflict, "There is already a channel %q.", name)
	}
	if ok, wait := s.createLimit.take(p.Name); !ok {
		return Channel{}, dto.Errorf(dto.CodeRateLimited, "You created %d channels in the last hour; wait %d s.",
			s.opts.Config.ChannelCreatePerHour, retrySeconds(wait)).RetryAfter(retrySeconds(wait))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback()
	now := s.clock.Now().UnixNano()
	res, err := tx.ExecContext(ctx, `INSERT INTO channels(name, title, topic, surface, created_by, created_at, last_activity, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'runtime') ON CONFLICT(name) DO NOTHING`,
		name, nullStr(req.Title), nullStr(req.Topic), string(surface), p.Name, now, now)
	if err != nil {
		return Channel{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Channel{}, dto.Errorf(dto.CodeConflict, "There is already a channel %q.", name)
	}
	added := uniq(req.Patterns)
	for _, pat := range added {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_streams(channel, pattern) VALUES (?, ?)`, name, pat); err != nil {
			return Channel{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Channel{}, err
	}
	if err := s.setFollow(ctx, p.Name, "chan:"+name, true); err != nil {
		return Channel{}, err
	}
	if len(added) > 0 {
		if err := s.marker(ctx, "chan:"+name, p.Name, p.Name+" bound "+strings.Join(added, ", ")); err != nil {
			return Channel{}, err
		}
	}
	s.emitChat("chan:"+name, "")
	return s.Channel(ctx, name)
}

func uniq(in []string) []string {
	var out []string
	for _, v := range in {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// UpdateChannel changes a channel. Bindings, title, topic and surface are
// for anyone with the message scope; renaming and archiving for the
// creator or an admin, on runtime channels.
func (s *Service) UpdateChannel(ctx context.Context, p auth.Principal, name string, u dto.ChannelUpdate) (Channel, error) {
	if err := needMessage(p); err != nil {
		return Channel{}, err
	}
	ch, err := s.Channel(ctx, name)
	if err != nil {
		return Channel{}, err
	}
	if (u.Rename != "" || u.Archived != nil) && !ch.CanManage(p) {
		if ch.Source != dto.SourceRuntime {
			return Channel{}, dto.Errorf(dto.CodeForbidden, "#%s comes from the configuration; it cannot be renamed or archived.", ch.Name)
		}
		return Channel{}, dto.Errorf(dto.CodeForbidden, "Only %s, who created #%s, or an admin may rename or archive it.", ch.CreatedBy, ch.Name)
	}
	if err := checkTexts(u.Title, u.Topic); err != nil {
		return Channel{}, err
	}
	if err := checkPatterns(append(slices.Clone(u.AddPatterns), u.RemovePatterns...)); err != nil {
		return Channel{}, err
	}
	if ch.Name == General && len(u.AddPatterns) > 0 {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "#general has no bindings; create a channel for a device.")
	}
	if u.Surface != "" && !validSurface(u.Surface) {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "surface %q is not none, markers or errors.", u.Surface)
	}
	newName := ""
	if u.Rename != "" {
		newName = normalizeChannel(u.Rename)
		if err := ValidateChannelName(newName); err != nil {
			return Channel{}, dto.Errorf(dto.CodeBadRequest, "%v.", err)
		}
		if newName == ch.Name {
			newName = ""
		} else if _, err := s.channel(ctx, newName); err == nil {
			return Channel{}, dto.Errorf(dto.CodeConflict, "There is already a channel %q.", newName)
		}
	}

	var added, removed []string
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback()
	if u.Title != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET title = ? WHERE name = ?`, nullStr(*u.Title), ch.Name); err != nil {
			return Channel{}, err
		}
	}
	if u.Topic != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET topic = ? WHERE name = ?`, nullStr(*u.Topic), ch.Name); err != nil {
			return Channel{}, err
		}
	}
	if u.Surface != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET surface = ? WHERE name = ?`, string(u.Surface), ch.Name); err != nil {
			return Channel{}, err
		}
	}
	for _, pat := range uniq(u.RemovePatterns) {
		r, err := tx.ExecContext(ctx, `DELETE FROM channel_streams WHERE channel = ? AND pattern = ?`, ch.Name, pat)
		if err != nil {
			return Channel{}, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			removed = append(removed, pat)
		}
	}
	for _, pat := range uniq(u.AddPatterns) {
		r, err := tx.ExecContext(ctx, `INSERT INTO channel_streams(channel, pattern) VALUES (?, ?) ON CONFLICT DO NOTHING`, ch.Name, pat)
		if err != nil {
			return Channel{}, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			added = append(added, pat)
		}
	}
	if u.Archived != nil {
		var at any
		if *u.Archived {
			at = s.clock.Now().UnixNano()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET archived_at = ?, last_activity = ? WHERE name = ?`,
			at, s.clock.Now().UnixNano(), ch.Name); err != nil {
			return Channel{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Channel{}, err
	}

	final := ch.Name
	if newName != "" {
		if err := s.rename(ctx, ch.Name, newName); err != nil {
			return Channel{}, err
		}
		final = newName
		if err := s.marker(ctx, "chan:"+final, p.Name, fmt.Sprintf("%s renamed #%s to #%s", p.Name, ch.Name, final)); err != nil {
			return Channel{}, err
		}
	}
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "bound "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "unbound "+strings.Join(removed, ", "))
	}
	if len(parts) > 0 {
		if err := s.marker(ctx, "chan:"+final, p.Name, p.Name+" "+strings.Join(parts, "; ")); err != nil {
			return Channel{}, err
		}
	}
	if final != ch.Name {
		s.emit(ChatEvent{Kind: EventChat, Stream: ch.Stream(), Gone: true})
	}
	s.emitChat("chan:"+final, "")
	return s.Channel(ctx, final)
}

func (s *Service) needExec() error {
	if s.exec == nil {
		return errors.New("messaging: the writer cannot run transactions")
	}
	return nil
}

// rename moves a channel and its messages to a new name, in the writer
// because entries change stream.
func (s *Service) rename(ctx context.Context, from, to string) error {
	if err := s.needExec(); err != nil {
		return err
	}
	return s.exec.Exec(ctx, func(ctx context.Context, tx *sql.Tx) error {
		stmts := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO channels(name, title, topic, surface, created_by, created_at, last_activity, archived_at, source)
				SELECT ?, title, topic, surface, created_by, created_at, last_activity, archived_at, source FROM channels WHERE name = ?`, []any{to, from}},
			{`UPDATE channel_streams SET channel = ? WHERE channel = ?`, []any{to, from}},
			{`INSERT INTO streams(id, kind, labels, created_at, last_seq, retention)
				SELECT ?, kind, labels, created_at, last_seq, retention FROM streams WHERE id = ? ON CONFLICT(id) DO NOTHING`, []any{"chan:" + to, "chan:" + from}},
			{`UPDATE entries SET stream = ? WHERE stream = ?`, []any{"chan:" + to, "chan:" + from}},
			{`DELETE FROM streams WHERE id = ?`, []any{"chan:" + from}},
			{`DELETE FROM channels WHERE name = ?`, []any{from}},
			{`UPDATE follows SET chat = ? WHERE chat = ?`, []any{"chan:" + to, "chan:" + from}},
		}
		for _, st := range stmts {
			if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteChannel deletes a runtime channel and its messages.
func (s *Service) DeleteChannel(ctx context.Context, p auth.Principal, name string) error {
	if err := needMessage(p); err != nil {
		return err
	}
	ch, err := s.Channel(ctx, name)
	if err != nil {
		return err
	}
	if !ch.CanManage(p) {
		if ch.Source != dto.SourceRuntime {
			return dto.Errorf(dto.CodeForbidden, "#%s comes from the configuration; it cannot be deleted.", ch.Name)
		}
		return dto.Errorf(dto.CodeForbidden, "Only %s, who created #%s, or an admin may delete it.", ch.CreatedBy, ch.Name)
	}
	if err := s.needExec(); err != nil {
		return err
	}
	stream := ch.Stream()
	defer s.emit(ChatEvent{Kind: EventChat, Stream: stream, Gone: true})
	return s.exec.Exec(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []struct {
			q   string
			arg string
		}{
			{`DELETE FROM entries WHERE stream = ?`, stream},
			{`DELETE FROM streams WHERE id = ?`, stream},
			{`DELETE FROM channels WHERE name = ?`, ch.Name},
			{`DELETE FROM follows WHERE chat = ?`, stream},
		} {
			if _, err := tx.ExecContext(ctx, q.q, q.arg); err != nil {
				return err
			}
		}
		return nil
	})
}

// touchChannel records activity, which also unarchives the channel.
func (s *Service) touchChannel(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channels SET last_activity = ?, archived_at = NULL WHERE name = ?`, s.clock.Now().UnixNano(), name)
	return err
}

// ArchiveIdle archives the runtime channels with no message and no line
// on their bound streams for archive_after. It returns how many.
func (s *Service) ArchiveIdle(ctx context.Context) (int, error) {
	if s.opts.Config.ArchiveAfter <= 0 {
		return 0, nil
	}
	chs, err := s.queryChannels(ctx, `WHERE archived_at IS NULL AND source = 'runtime'`)
	if err != nil || len(chs) == 0 {
		return 0, err
	}
	streams, err := s.st.Streams(ctx)
	if err != nil {
		return 0, err
	}
	now := s.clock.Now()
	cutoff := now.Add(-s.opts.Config.ArchiveAfter)
	n := 0
	for _, ch := range chs {
		last := ch.LastActivity
		for _, st := range streams {
			if st.LastTS != nil && st.LastTS.After(last) && (st.ID == ch.Stream() || (!store.IsMessageStream(st.ID) && store.StreamMatches(st.ID, ch.Patterns) && len(ch.Patterns) > 0)) {
				last = *st.LastTS
			}
		}
		if last.After(cutoff) {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE channels SET archived_at = ? WHERE name = ? AND archived_at IS NULL`, now.UnixNano(), ch.Name); err != nil {
			return n, err
		}
		s.emitChat(ch.Stream(), "")
		n++
	}
	return n, nil
}

// marker writes a marker through the writer.
func (s *Service) marker(ctx context.Context, stream, sender, text string) error {
	_, err := s.w.Commit(ctx, store.Entry{Stream: stream, Kind: store.KindMarker, Severity: store.DefaultSeverity, Text: text, Sender: sender})
	return err
}
