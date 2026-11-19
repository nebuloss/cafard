package projects

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/store"
)

// Access is what one principal may reach: its grants, each capped by the
// scopes of the credential it came with. The admin session reaches
// everything; a sink token, and a principal with no grant, nothing.
type Access struct {
	all      bool
	name     string
	cap      auth.ScopeSet
	projects map[string]auth.ScopeSet // project grants
	channels map[string]auth.ScopeSet // "project/channel" grants
	bindings map[string][]string      // the patterns of each granted channel
}

// All reports whether this is the admin, who reaches everything.
func (a *Access) All() bool { return a.all }

// Projects are the projects with any grant, by name; nil for the admin,
// who has them all.
func (a *Access) Projects() []string {
	if a.all {
		return nil
	}
	seen := map[string]bool{}
	for p := range a.projects {
		seen[p] = true
	}
	for ch := range a.channels {
		p, _, _ := strings.Cut(ch, "/")
		seen[p] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Sees reports whether a has any grant in project.
func (a *Access) Sees(project string) bool {
	return a.all || slices.Contains(a.Projects(), project)
}

// ProjectScopes are the scopes of the grant on the whole project.
func (a *Access) ProjectScopes(project string) auth.ScopeSet {
	if a.all {
		return a.cap
	}
	return a.projects[project]
}

// Scopes are what a may do on a stream: a channel, a log stream or a
// direct conversation.
func (a *Access) Scopes(stream string) auth.ScopeSet {
	if a.all {
		return a.cap
	}
	if rest, ok := strings.CutPrefix(stream, "dm:"); ok {
		x, y, _ := strings.Cut(rest, "~")
		if a.name != "" && (a.name == x || a.name == y) {
			return a.cap
		}
		return nil
	}
	project := store.ProjectOf(stream)
	if project == "" {
		return nil
	}
	out := append(auth.ScopeSet{}, a.projects[project]...)
	if local, ok := strings.CutPrefix(stream, "chan:"); ok {
		out = append(out, a.channels[local]...)
		return auth.NewScopeSet(out...)
	}
	for ch, patterns := range a.bindings {
		if strings.HasPrefix(ch, project+"/") && store.StreamMatches(stream, patterns) {
			out = append(out, a.channels[ch]...)
		}
	}
	return auth.NewScopeSet(out...)
}

// Can reports whether a may use scope on stream.
func (a *Access) Can(stream string, scope auth.Scope) bool { return a.Scopes(stream).Has(scope) }

// CanChannel is Can on the channel "project/name".
func (a *Access) CanChannel(name string, scope auth.Scope) bool { return a.Can("chan:"+name, scope) }

// LogPatterns are globs of the log streams a may read, for the hub and the
// store; nil means every one (the admin), empty none.
func (a *Access) LogPatterns() []string {
	if a.all {
		return nil
	}
	out := []string{}
	for p, sc := range a.projects {
		if sc.Has(auth.ScopeRead) {
			out = append(out, "*:"+p+"/*")
		}
	}
	for ch, sc := range a.channels {
		if sc.Has(auth.ScopeRead) {
			out = append(out, a.bindings[ch]...)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// ChannelPatterns are the chan: globs of the channels a may read; nil
// means every one (the admin), empty none.
func (a *Access) ChannelPatterns() []string {
	if a.all {
		return nil
	}
	out := []string{}
	for p, sc := range a.projects {
		if sc.Has(auth.ScopeRead) {
			out = append(out, "chan:"+p+"/*")
		}
	}
	for ch, sc := range a.channels {
		if sc.Has(auth.ScopeRead) {
			out = append(out, "chan:"+ch)
		}
	}
	sort.Strings(out)
	return out
}

// MessagePatterns are the message streams a may read: its channels and
// the direct conversations, which the caller still checks one by one.
func (a *Access) MessagePatterns() []string {
	if a.all {
		return []string{"chan:*/*", "dm:*"}
	}
	return append(a.ChannelPatterns(), "dm:*")
}

// accessTTL bounds how long a cached Access lives, so grants written by
// another process (cafard token create --grant) apply without a restart.
const accessTTL = 5 * time.Second

type accessCache struct {
	mu      sync.Mutex
	gen     uint64 // bumped by Invalidate, so a load that raced it is not kept
	entries map[string]cachedAccess
}

type cachedAccess struct {
	a  *Access
	at time.Time
}

func (c *accessCache) init() { c.entries = map[string]cachedAccess{} }

// Invalidate drops every cached Access; every write through the service
// calls it, and messaging does after a change to channels or bindings.
func (s *Service) Invalidate() {
	s.cache.mu.Lock()
	s.cache.gen++
	s.cache.entries = map[string]cachedAccess{}
	s.cache.mu.Unlock()
}

// Access is what p may reach. A database error denies everything for this
// call and is logged.
func (s *Service) Access(ctx context.Context, p auth.Principal) *Access {
	if p.Has(auth.ScopeAdmin) {
		return &Access{all: true, name: p.Name, cap: p.Scopes}
	}
	empty := &Access{name: p.Name, cap: p.Scopes}
	if p.Name == "" || p.Sink != "" {
		return empty
	}
	key := p.Name + "\x00" + p.Scopes.String()
	now := s.clock.Now()
	s.cache.mu.Lock()
	c, ok := s.cache.entries[key]
	gen := s.cache.gen
	s.cache.mu.Unlock()
	if ok && now.Sub(c.at) < accessTTL && now.Sub(c.at) >= 0 {
		return c.a
	}
	a, err := s.load(ctx, p)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("loading grants failed", "principal", p.Name, "err", err)
		}
		return empty
	}
	s.cache.mu.Lock()
	if s.cache.gen == gen {
		s.cache.entries[key] = cachedAccess{a: a, at: now}
	}
	s.cache.mu.Unlock()
	return a
}

func (s *Service) load(ctx context.Context, p auth.Principal) (*Access, error) {
	grants, err := s.Grants(ctx, "", p.Name)
	if err != nil {
		return nil, err
	}
	a := &Access{name: p.Name, cap: p.Scopes, projects: map[string]auth.ScopeSet{},
		channels: map[string]auth.ScopeSet{}, bindings: map[string][]string{}}
	for _, g := range grants {
		var capped []auth.Scope
		for _, sc := range g.Scopes {
			if p.Scopes.Has(sc) {
				capped = append(capped, sc)
			}
		}
		if len(capped) == 0 {
			continue
		}
		if g.Channel == "" {
			a.projects[g.Project] = auth.NewScopeSet(capped...)
			continue
		}
		ch := g.Target()
		a.channels[ch] = auth.NewScopeSet(capped...)
		patterns, err := store.QueryColumn[string](ctx, s.db, `SELECT pattern FROM channel_streams WHERE channel = ?`, ch)
		if err != nil {
			return nil, err
		}
		a.bindings[ch] = patterns
	}
	return a, nil
}

// Share reports whether two principals have a grant in a common project;
// the admin shares every project.
func (s *Service) Share(ctx context.Context, a, b string) (bool, error) {
	if a == auth.AdminName || b == auth.AdminName {
		return true, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM grants g1 JOIN grants g2 ON g1.project = g2.project
		WHERE g1.principal = ? AND g2.principal = ?`, a, b).Scan(&n)
	return n > 0, err
}

// Reachable keeps of rows the principals p may message: those sharing a
// project with p, and the admin; every one for the admin.
func (s *Service) Reachable(ctx context.Context, p auth.Principal, rows []auth.PrincipalRow) ([]auth.PrincipalRow, error) {
	if s.Access(ctx, p).All() {
		return rows, nil
	}
	peers, err := store.QueryColumn[string](ctx, s.db, `SELECT DISTINCT g2.principal FROM grants g1 JOIN grants g2 ON g1.project = g2.project
		WHERE g1.principal = ? AND g2.principal <> ?`, p.Name, p.Name)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(rows, func(r auth.PrincipalRow) bool {
		return r.Name != auth.AdminName && !slices.Contains(peers, r.Name)
	}), nil
}
