package messaging

import (
	"context"
	"slices"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/projects"
)

// Private channels (DECISIONS 2026-11-20, "private channels"): a chat or
// a gallery in its creator's personal space (projects.Personal), read by
// the creator and the members they added. A member must be someone the
// creator may reach already (a shared project, as for a direct message),
// so adding people reveals no one and widens no one's reach beyond the
// channel's own members.

// maxMembers bounds the members added at once.
const maxMembers = 50

// createPrivate is CreateChannel with Private: the channel in p's
// personal space, a read and message grant on it for each member, who
// follows it.
func (s *Service) createPrivate(ctx context.Context, p auth.Principal, req dto.ChannelCreate) (Channel, error) {
	space := projects.PersonalOf(p.Name)
	local := normalizeChannel(req.Name)
	if pr, l, ok := strings.Cut(local, "/"); ok {
		if pr != space {
			return Channel{}, dto.Errorf(dto.CodeBadRequest, "A private channel is in your own space (%s/%s).", space, l)
		}
		local = l
	}
	if req.Project != "" && req.Project != space {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "A private channel is in your own space, not in project %s.", req.Project)
	}
	if len(req.Patterns) > 0 {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "A private channel binds no stream; make the channel in a project to follow its logs.")
	}
	if err := ValidateChannelName(local); err != nil {
		return Channel{}, dto.Errorf(dto.CodeBadRequest, "%v.", err)
	}
	members, err := s.reachableMembers(ctx, p, req.Members)
	if err != nil {
		return Channel{}, err
	}
	if _, err := s.projects.EnsurePersonal(ctx, p.Name); err != nil {
		return Channel{}, err
	}
	ch, err := s.CreateChannel(ctx, p, dto.ChannelCreate{Name: local, Project: space, Title: req.Title, Topic: req.Topic, Kind: req.Kind})
	if err != nil {
		return Channel{}, err
	}
	if err := s.addMembers(ctx, ch, members); err != nil {
		return Channel{}, err
	}
	return ch, nil
}

// reachableMembers checks the names p adds: each one a principal p may
// reach. An unknown name and one p may not reach get the same answer.
func (s *Service) reachableMembers(ctx context.Context, p auth.Principal, names []string) ([]string, error) {
	var want []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" && n != p.Name && !slices.Contains(want, n) {
			want = append(want, n)
		}
	}
	if len(want) > maxMembers {
		return nil, dto.Errorf(dto.CodeBadRequest, "A private channel starts with %d members at most.", maxMembers)
	}
	if len(want) == 0 {
		return nil, nil
	}
	rows, err := s.tokens.Principals(ctx, "")
	if err != nil {
		return nil, err
	}
	rows = slices.DeleteFunc(rows, func(r auth.PrincipalRow) bool { return !slices.Contains(want, r.Name) })
	reach, err := s.projects.Reachable(ctx, p, rows)
	if err != nil {
		return nil, err
	}
	for _, n := range want {
		if !slices.ContainsFunc(reach, func(r auth.PrincipalRow) bool { return r.Name == n }) {
			return nil, dto.Errorf(dto.CodeNotFound, "There is no principal %s you can reach; members share a project with you.", n)
		}
	}
	return want, nil
}

// addMembers grants read and message on ch to each member, who follows it.
func (s *Service) addMembers(ctx context.Context, ch Channel, members []string) error {
	if len(members) == 0 {
		return nil
	}
	gs := make([]projects.Grant, len(members))
	for i, m := range members {
		gs[i] = projects.Grant{Principal: m, Project: ch.Project(), Channel: ch.Local(), Scopes: projects.PersonalScopes}
	}
	if err := s.projects.AddGrants(ctx, gs...); err != nil {
		return err
	}
	for _, m := range members {
		if err := s.setFollow(ctx, m, ch.Stream(), true); err != nil {
			return err
		}
	}
	s.emitChat(ch.Stream(), "")
	return nil
}

// PrivateCandidates are the principals p may add to a private channel:
// those it may reach, but itself, by name.
func (s *Service) PrivateCandidates(ctx context.Context, p auth.Principal) ([]auth.PrincipalRow, error) {
	rows, err := s.tokens.Principals(ctx, "")
	if err != nil {
		return nil, err
	}
	rows, err = s.projects.Reachable(ctx, p, rows)
	if err != nil {
		return nil, err
	}
	rows = slices.DeleteFunc(rows, func(r auth.PrincipalRow) bool { return r.Name == p.Name })
	slices.SortFunc(rows, func(a, b auth.PrincipalRow) int { return strings.Compare(a.Name, b.Name) })
	return rows, nil
}
