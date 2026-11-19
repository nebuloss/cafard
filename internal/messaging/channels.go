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
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/ratelimit"
	"github.com/nebuloss/cafard/internal/store"
)

// General is the channel every project has.
const General = "general"

// systemName is created_by for channels cafard makes itself.
const systemName = "cafard"

const (
	maxTitle = 200
	maxTopic = 1000
)

var channelNameRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// ValidateChannelName checks a new channel's name inside its project: a
// lowercase slug of 1 to 40 characters, not a reserved name.
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
	Name         string // "project/name"
	Kind         dto.ChannelKind
	Title, Topic string
	Surface      dto.Surface
	CreatedBy    string
	LastActivity time.Time
	ArchivedAt   *time.Time
	Source       dto.ChatSource
	Patterns     []string
}

// Stream is the channel's message stream.
func (c Channel) Stream() string { return "chan:" + c.Name }

// Gallery reports whether the channel is a gallery.
func (c Channel) Gallery() bool { return c.Kind == dto.ChannelGallery }

// Project is the project the channel belongs to.
func (c Channel) Project() string { return store.ProjectOf(c.Stream()) }

// Local is the channel's name inside its project.
func (c Channel) Local() string { return store.LocalName(c.Stream()) }

// Archived reports whether the channel is archived.
func (c Channel) Archived() bool { return c.ArchivedAt != nil }

// CanManage reports whether p, reaching a, may rename, archive or delete c:
// its creator or the admin, with message on the whole project, on a
// runtime channel.
func (c Channel) CanManage(p auth.Principal, a *projects.Access) bool {
	return c.Source == dto.SourceRuntime && (p.Name == c.CreatedBy || p.Has(auth.ScopeAdmin)) &&
		a.ProjectScopes(c.Project()).Has(auth.ScopeMessage)
}

// normalizeChannel accepts "dut1", "#dut1", "lab/dut1" and "chan:lab/dut1".
func normalizeChannel(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "#")
	return strings.TrimPrefix(name, "chan:")
}

var errNoChannel = errors.New("no such channel")

// channel looks up a channel by its full "project/name".
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

// Channel returns the channel p names and may read: "project/name", or a
// bare name only one project p reaches has. Anything else is an
// unknown_channel error listing the channels p can read.
func (s *Service) Channel(ctx context.Context, p auth.Principal, name string) (Channel, error) {
	return s.channelFor(ctx, p, name, auth.ScopeRead)
}

// channelFor is Channel for a use that needs scope on the channel: a bare
// name in several projects picks the one where p has it, and a channel p
// reads without it is forbidden.
func (s *Service) channelFor(ctx context.Context, p auth.Principal, name string, scope auth.Scope) (Channel, error) {
	name = normalizeChannel(name)
	a := s.access(ctx, p)
	var found []Channel
	if strings.Contains(name, "/") {
		ch, err := s.channel(ctx, name)
		if err != nil && !errors.Is(err, errNoChannel) {
			return Channel{}, err
		}
		if err == nil && a.CanChannel(ch.Name, auth.ScopeRead) {
			found = []Channel{ch}
		}
	} else {
		chs, err := s.queryChannels(ctx, `WHERE substr(name, instr(name, '/') + 1) = ?`, name)
		if err != nil {
			return Channel{}, err
		}
		for _, ch := range chs {
			if a.CanChannel(ch.Name, auth.ScopeRead) {
				found = append(found, ch)
			}
		}
	}
	if len(found) > 1 {
		usable := slices.DeleteFunc(slices.Clone(found), func(ch Channel) bool { return !a.CanChannel(ch.Name, scope) })
		if len(usable) == 1 {
			return usable[0], nil
		}
		names := make([]string, len(found))
		for i, ch := range found {
			names[i] = ch.Name
		}
		return Channel{}, dto.Errorf(dto.CodeUnknownChannel, "Several projects have a channel %q: %s. Name one as project/%s.",
			name, strings.Join(names, ", "), name)
	}
	if len(found) == 0 {
		return Channel{}, s.unknownChannel(ctx, p, name)
	}
	if !a.CanChannel(found[0].Name, scope) {
		return Channel{}, dto.Errorf(dto.CodeForbidden, "You may read #%s but not %s there; ask the admin for a grant.", found[0].Name, scopeVerb(scope))
	}
	return found[0], nil
}

func scopeVerb(scope auth.Scope) string {
	switch scope {
	case auth.ScopeMessage:
		return "post"
	case auth.ScopeSerialWrite:
		return "drive serial ports"
	}
	return string(scope)
}

// Channels lists the channels p may read, by name.
func (s *Service) Channels(ctx context.Context, p auth.Principal, includeArchived bool) ([]Channel, error) {
	chs, err := s.allChannels(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	a := s.access(ctx, p)
	return slices.DeleteFunc(chs, func(ch Channel) bool { return !a.CanChannel(ch.Name, auth.ScopeRead) }), nil
}

func (s *Service) allChannels(ctx context.Context, includeArchived bool) ([]Channel, error) {
	if includeArchived {
		return s.queryChannels(ctx, ``)
	}
	return s.queryChannels(ctx, `WHERE archived_at IS NULL`)
}

func (s *Service) queryChannels(ctx context.Context, where string, args ...any) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, kind, coalesce(title, ''), coalesce(topic, ''), surface, created_by,
		last_activity, archived_at, source FROM channels `+where+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	var out []Channel
	for rows.Next() {
		var (
			c                   Channel
			activity            int64
			archived            sql.NullInt64
			surface, sourceName string
			kind                string
		)
		if err := rows.Scan(&c.Name, &kind, &c.Title, &c.Topic, &surface, &c.CreatedBy, &activity, &archived, &sourceName); err != nil {
			rows.Close()
			return nil, err
		}
		c.Surface, c.Source, c.Kind = dto.Surface(surface), dto.ChatSource(sourceName), dto.ChannelKind(kind)
		c.LastActivity = time.Unix(0, activity).UTC()
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

// patterns are a channel's bindings, never nil: an empty list narrows to
// nothing where nil would mean every stream (list_claims).
func (s *Service) patterns(ctx context.Context, name string) ([]string, error) {
	out, err := store.QueryColumn[string](ctx, s.db, `SELECT pattern FROM channel_streams WHERE channel = ? ORDER BY pattern`, name)
	if out == nil && err == nil {
		out = []string{}
	}
	return out, err
}

func (s *Service) unknownChannel(ctx context.Context, p auth.Principal, name string) *dto.APIError {
	chs, err := s.Channels(ctx, p, true)
	var names []string
	if err == nil {
		for _, c := range chs {
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		return dto.Errorf(dto.CodeUnknownChannel, "There is no channel %q you can read; you have no grant on any channel yet.", normalizeChannel(name))
	}
	return dto.Errorf(dto.CodeUnknownChannel, "There is no channel %q you can read. Channels you can read: %s.", normalizeChannel(name), strings.Join(names, ", "))
}

// ResolveChannel returns the binding patterns of the channel p names:
// stream ids and globs, which the log tools and the hub match themselves,
// so a stream that appears during a wait_for is still seen. Reading the
// channel lets p read them. An agent that reads a channel this way
// follows it.
func (s *Service) ResolveChannel(ctx context.Context, p auth.Principal, name string) (string, []string, error) {
	ch, err := s.Channel(ctx, p, name)
	if err != nil {
		return "", nil, err
	}
	if p.Kind == auth.KindAgent {
		if err := s.autoFollow(ctx, p.Name, ch.Stream()); err != nil {
			return "", nil, err
		}
	}
	return ch.Stream(), slices.Clone(ch.Patterns), nil
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

// ensureGeneral makes the #general of a project, which every project
// has. It is a config channel: it cannot be renamed or deleted.
func (s *Service) ensureGeneral(ctx context.Context, project string) error {
	now := s.clock.Now().UnixNano()
	_, err := s.db.ExecContext(ctx, `INSERT INTO channels(name, surface, created_by, created_at, last_activity, source)
		VALUES (?, 'markers', ?, ?, ?, 'config') ON CONFLICT(name) DO NOTHING`, project+"/"+General, systemName, now, now)
	return err
}

// configName is a config channel's "project/name": a bare name is in the
// project imported.
func configName(name string) (project, local string) {
	if p, l, ok := strings.Cut(name, "/"); ok {
		return p, l
	}
	return projects.Imported, name
}

// applyConfigChannels makes sure every project has its #general and
// reapplies the `channels:` list, making the projects it names. A config
// channel that left the list becomes a runtime one, which its creator may
// then delete.
func (s *Service) applyConfigChannels(ctx context.Context) error {
	for _, c := range s.opts.Channels.List {
		project, _ := configName(c.Name)
		if _, err := s.projects.Ensure(ctx, project, ""); err != nil {
			return err
		}
	}
	all, err := s.projects.Projects(ctx)
	if err != nil {
		return err
	}
	listed := map[string]bool{}
	for _, p := range all {
		if err := s.ensureGeneral(ctx, p.Name); err != nil {
			return err
		}
		listed[p.Name+"/"+General] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.clock.Now().UnixNano()
	for _, c := range s.opts.Channels.List {
		project, local := configName(c.Name)
		name := project + "/" + local
		listed[name] = true
		if local == General {
			if _, err := tx.ExecContext(ctx, `UPDATE channels SET title = ?, topic = ? WHERE name = ?`,
				nullStr(c.Title), nullStr(c.Topic), name); err != nil {
				return err
			}
			continue
		}
		patterns, err := qualifyPatterns(project, c.Streams)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		surface := c.Surface
		if surface == "" {
			surface = dto.SurfaceMarkers
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO channels(name, title, topic, surface, created_by, created_at, last_activity, source)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'config')
			ON CONFLICT(name) DO UPDATE SET title = excluded.title, topic = excluded.topic, surface = excluded.surface,
			source = 'config', archived_at = NULL`,
			name, nullStr(c.Title), nullStr(c.Topic), string(surface), systemName, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM channel_streams WHERE channel = ?`, name); err != nil {
			return err
		}
		for _, p := range patterns {
			if _, err := tx.ExecContext(ctx, `INSERT INTO channel_streams(channel, pattern) VALUES (?, ?) ON CONFLICT DO NOTHING`, name, p); err != nil {
				return err
			}
		}
	}
	names, err := store.QueryColumn[string](ctx, tx, `SELECT name FROM channels WHERE source = 'config'`)
	if err != nil {
		return err
	}
	for _, n := range names {
		if listed[n] {
			continue
		}
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

// qualifyPatterns puts bindings inside project: serial:dut1 means
// serial:<project>/dut1. A binding that names another project is refused,
// so a channel never shows logs from outside its project.
func qualifyPatterns(project string, ps []string) ([]string, error) {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if err := validateBinding(p); err != nil {
			return nil, err
		}
		kind, rest, ok := strings.Cut(p, ":")
		if !ok {
			return nil, fmt.Errorf("binding %q is not kind:name, such as serial:dut1", p)
		}
		if other, _, found := strings.Cut(rest, "/"); !found {
			rest = project + "/" + rest
		} else if other != project {
			return nil, fmt.Errorf("binding %q names another project than %s; a channel binds streams of its own project", p, project)
		}
		out = append(out, kind+":"+rest)
	}
	return out, nil
}

func checkPatterns(project string, ps []string) ([]string, error) {
	out, err := qualifyPatterns(project, ps)
	if err != nil {
		return nil, dto.Errorf(dto.CodeInvalidPattern, "%v.", err)
	}
	return out, nil
}

func needMessage(p auth.Principal) error {
	if !p.Has(auth.ScopeMessage) {
		return auth.MissingScope(auth.ScopeMessage)
	}
	return nil
}

// needProjectMessage refuses unless p has message on the whole project:
// making, binding, renaming and deleting channels need it, so a channel
// grant cannot widen itself.
func needProjectMessage(a *projects.Access, project string) error {
	if a.ProjectScopes(project).Has(auth.ScopeMessage) {
		return nil
	}
	return dto.Errorf(dto.CodeForbidden, "Making, binding, renaming and deleting channels of %s needs message on the whole project; ask the admin for a grant.", project)
}

// newChannelProject is the project a new channel goes in: the one named,
// or the only one where p may make channels.
func (s *Service) newChannelProject(ctx context.Context, p auth.Principal, a *projects.Access, project, name string) (string, string, error) {
	if pr, local, ok := strings.Cut(name, "/"); ok {
		if project != "" && project != pr {
			return "", "", dto.Errorf(dto.CodeBadRequest, "The channel %s is not in project %s.", name, project)
		}
		project, name = pr, local
	}
	if project != "" {
		if _, err := s.projects.Project(ctx, project); err != nil || !a.Sees(project) {
			return "", "", dto.Errorf(dto.CodeNotFound, "There is no project %s you can reach.", project)
		}
		return project, name, needProjectMessage(a, project)
	}
	candidates := a.Projects()
	if a.All() {
		all, err := s.projects.Projects(ctx)
		if err != nil {
			return "", "", err
		}
		for _, pr := range all {
			candidates = append(candidates, pr.Name)
		}
	}
	candidates = slices.DeleteFunc(candidates, func(pr string) bool { return !a.ProjectScopes(pr).Has(auth.ScopeMessage) })
	switch len(candidates) {
	case 0:
		return "", "", dto.Errorf(dto.CodeForbidden, "You may make channels in no project: that needs message on a whole project; ask the admin for a grant.")
	case 1:
		return candidates[0], name, nil
	}
	return "", "", dto.Errorf(dto.CodeBadRequest, "Name the project of the new channel (project/%s): you may make channels in %s.", name, strings.Join(candidates, ", "))
}

// CreateChannel creates a runtime channel; its creator follows it.
func (s *Service) CreateChannel(ctx context.Context, p auth.Principal, req dto.ChannelCreate) (Channel, error) {
	if err := needMessage(p); err != nil {
		return Channel{}, err
	}
	a := s.access(ctx, p)
	project, local, err := s.newChannelProject(ctx, p, a, req.Project, normalizeChannel(req.Name))
	if err != nil {
		return Channel{}, err
	}
	if err := ValidateChannelName(local); err != nil {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "%v.", err)
	}
	name := project + "/" + local
	if err := checkTexts(&req.Title, &req.Topic); err != nil {
		return Channel{}, err
	}
	patterns, err := checkPatterns(project, req.Patterns)
	if err != nil {
		return Channel{}, err
	}
	surface := req.Surface
	if surface == "" {
		surface = dto.SurfaceMarkers
	}
	if !validSurface(surface) {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "surface %q is not none, markers or errors.", surface)
	}
	kind, err := channelKind(req.Kind)
	if err != nil {
		return Channel{}, err
	}
	if _, err := s.channel(ctx, name); err == nil {
		return Channel{}, dto.Errorf(dto.CodeConflict, "There is already a channel %q.", name)
	}
	if ok, wait := s.createLimit.Take(p.Name); !ok {
		return Channel{}, dto.Errorf(dto.CodeRateLimited, "You created %d channels in the last hour; wait %d s.",
			s.opts.Config.ChannelCreatePerHour, ratelimit.RetrySeconds(wait)).RetryAfter(ratelimit.RetrySeconds(wait))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback()
	now := s.clock.Now().UnixNano()
	res, err := tx.ExecContext(ctx, `INSERT INTO channels(name, kind, title, topic, surface, created_by, created_at, last_activity, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'runtime') ON CONFLICT(name) DO NOTHING`,
		name, string(kind), nullStr(req.Title), nullStr(req.Topic), string(surface), p.Name, now, now)
	if err != nil {
		return Channel{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Channel{}, dto.Errorf(dto.CodeConflict, "There is already a channel %q.", name)
	}
	added := uniq(patterns)
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
	s.projects.Invalidate()
	return s.Channel(ctx, p, name)
}

// channelKind checks a requested kind; "" is a chat.
func channelKind(k dto.ChannelKind) (dto.ChannelKind, error) {
	switch k {
	case "":
		return dto.ChannelChat, nil
	case dto.ChannelChat, dto.ChannelGallery:
		return k, nil
	}
	return "", dto.Errorf(dto.CodeBadRequest, "A channel kind is chat or gallery, not %q.", k)
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

// UpdateChannel changes a channel. Title, topic and surface are for
// anyone who may post in it; bindings need message on the whole project;
// renaming and archiving are for the creator or the admin besides, on
// runtime channels.
func (s *Service) UpdateChannel(ctx context.Context, p auth.Principal, name string, u dto.ChannelUpdate) (Channel, error) {
	if err := needMessage(p); err != nil {
		return Channel{}, err
	}
	ch, err := s.channelFor(ctx, p, name, auth.ScopeMessage)
	if err != nil {
		return Channel{}, err
	}
	a := s.access(ctx, p)
	if len(u.AddPatterns)+len(u.RemovePatterns) > 0 || u.Rename != "" || u.Archived != nil || u.Kind != "" {
		if err := needProjectMessage(a, ch.Project()); err != nil {
			return Channel{}, err
		}
	}
	if (u.Rename != "" || u.Archived != nil) && !ch.CanManage(p, a) {
		if ch.Source != dto.SourceRuntime {
			return Channel{}, dto.Errorf(dto.CodeForbidden, "#%s comes from the configuration; it cannot be renamed or archived.", ch.Name)
		}
		return Channel{}, dto.Errorf(dto.CodeForbidden, "Only %s, who created #%s, or an admin may rename or archive it.", ch.CreatedBy, ch.Name)
	}
	if err := checkTexts(u.Title, u.Topic); err != nil {
		return Channel{}, err
	}
	addPatterns, err := checkPatterns(ch.Project(), u.AddPatterns)
	if err != nil {
		return Channel{}, err
	}
	removePatterns, err := checkPatterns(ch.Project(), u.RemovePatterns)
	if err != nil {
		return Channel{}, err
	}
	if ch.Local() == General && len(addPatterns) > 0 {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "#general has no bindings; create a channel for a device.")
	}
	if u.Surface != "" && !validSurface(u.Surface) {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "surface %q is not none, markers or errors.", u.Surface)
	}
	if u.Kind != "" {
		if _, err := channelKind(u.Kind); err != nil {
			return Channel{}, err
		}
		if ch.Local() == General && u.Kind == dto.ChannelGallery {
			return Channel{}, dto.Errorf(dto.CodeBadRequest, "#general stays a chat; make a gallery channel for screenshots.")
		}
	}
	newName := ""
	if u.Rename != "" {
		local := normalizeChannel(u.Rename)
		if pr, l, ok := strings.Cut(local, "/"); ok {
			if pr != ch.Project() {
				return Channel{}, dto.Errorf(dto.CodeBadRequest, "A channel stays in its project; rename it to a name inside %s.", ch.Project())
			}
			local = l
		}
		if err := ValidateChannelName(local); err != nil {
			return Channel{}, dto.Errorf(dto.CodeBadRequest, "%v.", err)
		}
		newName = ch.Project() + "/" + local
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
	if u.Kind != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET kind = ? WHERE name = ?`, string(u.Kind), ch.Name); err != nil {
			return Channel{}, err
		}
	}
	for _, pat := range uniq(removePatterns) {
		r, err := tx.ExecContext(ctx, `DELETE FROM channel_streams WHERE channel = ? AND pattern = ?`, ch.Name, pat)
		if err != nil {
			return Channel{}, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			removed = append(removed, pat)
		}
	}
	for _, pat := range uniq(addPatterns) {
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
	if u.Kind != "" && u.Kind != ch.Kind {
		parts = append(parts, "made it a "+string(u.Kind))
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
	s.projects.Invalidate()
	return s.Channel(ctx, p, final)
}

// rename moves a channel and its messages to a new name, in the writer
// because entries change stream.
func (s *Service) rename(ctx context.Context, from, to string) error {
	return s.exec.Exec(ctx, func(ctx context.Context, tx *sql.Tx) error {
		stmts := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO channels(name, kind, title, topic, surface, created_by, created_at, last_activity, archived_at, source)
				SELECT ?, kind, title, topic, surface, created_by, created_at, last_activity, archived_at, source FROM channels WHERE name = ?`, []any{to, from}},
			{`UPDATE channel_streams SET channel = ? WHERE channel = ?`, []any{to, from}},
			{`INSERT INTO streams(id, kind, labels, created_at, last_seq, retention)
				SELECT ?, kind, labels, created_at, last_seq, retention FROM streams WHERE id = ? ON CONFLICT(id) DO NOTHING`, []any{"chan:" + to, "chan:" + from}},
			{`UPDATE entries SET stream = ? WHERE stream = ?`, []any{"chan:" + to, "chan:" + from}},
			{`DELETE FROM streams WHERE id = ?`, []any{"chan:" + from}},
			{`DELETE FROM channels WHERE name = ?`, []any{from}},
			{`UPDATE follows SET chat = ? WHERE chat = ?`, []any{"chan:" + to, "chan:" + from}},
			// Grants on the channel follow it (the projects package owns the
			// table; this has to be in the same transaction).
			{`UPDATE grants SET channel = ? WHERE project = ? AND channel = ?`, []any{store.LocalName("chan:" + to), store.ProjectOf("chan:" + from), store.LocalName("chan:" + from)}},
		}
		for _, st := range stmts {
			if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteChannel deletes a runtime channel, its messages and the grants on
// it.
func (s *Service) DeleteChannel(ctx context.Context, p auth.Principal, name string) error {
	if err := needMessage(p); err != nil {
		return err
	}
	ch, err := s.channelFor(ctx, p, name, auth.ScopeMessage)
	if err != nil {
		return err
	}
	a := s.access(ctx, p)
	if err := needProjectMessage(a, ch.Project()); err != nil {
		return err
	}
	if !ch.CanManage(p, a) {
		if ch.Source != dto.SourceRuntime {
			return dto.Errorf(dto.CodeForbidden, "#%s comes from the configuration; it cannot be deleted.", ch.Name)
		}
		return dto.Errorf(dto.CodeForbidden, "Only %s, who created #%s, or an admin may delete it.", ch.CreatedBy, ch.Name)
	}
	stream := ch.Stream()
	defer s.emit(ChatEvent{Kind: EventChat, Stream: stream, Gone: true})
	defer s.projects.Invalidate()
	return s.exec.Exec(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []struct {
			q    string
			args []any
		}{
			{`DELETE FROM entries WHERE stream = ?`, []any{stream}},
			{`DELETE FROM streams WHERE id = ?`, []any{stream}},
			{`DELETE FROM channels WHERE name = ?`, []any{ch.Name}},
			{`DELETE FROM follows WHERE chat = ?`, []any{stream}},
			{`DELETE FROM grants WHERE project = ? AND channel = ?`, []any{ch.Project(), ch.Local()}},
		} {
			if _, err := tx.ExecContext(ctx, q.q, q.args...); err != nil {
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
