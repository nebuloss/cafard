package messaging

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/projects"
)

// Online tells whether a principal counts as online: a live feed open, or
// a last request recent enough. The web layer knows the feeds.
type Online func(name string, lastSeen *time.Time, now time.Time) bool

// memberOrder sorts the members panel: people, then agents, then devices.
var memberOrder = map[dto.PrincipalKind]int{dto.KindHuman: 0, dto.KindAgent: 1, dto.KindDevice: 2}

// ChannelMembers are the principals who can read the channel name (a
// read grant on its project or on the channel itself, and the admin),
// kept to those viewer may know about by list_principals' rule, with the
// viewer (always online). A channel viewer cannot read is unknown_channel, as for every
// other channel call.
func (s *Service) ChannelMembers(ctx context.Context, viewer auth.Principal, name string, online Online) ([]dto.Member, error) {
	ch, err := s.Channel(ctx, viewer, name)
	if err != nil {
		return nil, err
	}
	grants, err := s.projects.Grants(ctx, ch.Project(), "")
	if err != nil {
		return nil, err
	}
	// The admin reads every channel, but those of a personal space it was
	// not added to.
	reads := map[string]bool{auth.AdminName: !projects.Personal(ch.Project())}
	for _, g := range grants {
		if (g.Channel == "" || g.Channel == ch.Local()) && g.Scopes.Has(auth.ScopeRead) {
			reads[g.Principal] = true
		}
	}
	rows, err := s.tokens.Principals(ctx, "")
	if err != nil {
		return nil, err
	}
	rows = slices.DeleteFunc(rows, func(r auth.PrincipalRow) bool { return !reads[r.Name] })
	// Reachable leaves the viewer out: it lists whom the viewer may reach.
	known, err := s.projects.Reachable(ctx, viewer, slices.Clone(rows))
	if err != nil {
		return nil, err
	}
	isViewer := func(r auth.PrincipalRow) bool { return r.Name == viewer.Name }
	if i := slices.IndexFunc(rows, isViewer); i >= 0 && !slices.ContainsFunc(known, isViewer) {
		known = append(known, rows[i])
	}
	now := s.clock.Now()
	out := make([]dto.Member, 0, len(known))
	for _, r := range known {
		fs, err := s.Follow(ctx, auth.Principal{Name: r.Name, Kind: r.Kind}, ch.Stream())
		if err != nil {
			return nil, err
		}
		m := dto.Member{Name: r.Name, Kind: dto.PrincipalKind(r.Kind), Following: fs.Followed}
		if r.LastSeen != nil {
			m.LastSeen = dto.TimePtr(*r.LastSeen)
		}
		// The viewer is reading the panel, so online whatever its last
		// request says.
		m.Online = r.Name == viewer.Name || (online != nil && online(r.Name, r.LastSeen, now))
		if r.Name != viewer.Name {
			if m.CanDM, err = s.projects.Share(ctx, viewer.Name, r.Name); err != nil {
				return nil, err
			}
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b dto.Member) int {
		if c := cmp.Compare(memberOrder[a.Kind], memberOrder[b.Kind]); c != 0 {
			return c
		}
		if a.Online != b.Online {
			if a.Online {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out, nil
}
