package messaging

import (
	"context"
	"fmt"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/projects"
)

// Projects are the projects p has a grant in (every one for the admin),
// with p's scopes, the channels p can read and, when p may read the
// project's logs, its sinks.
func (s *Service) Projects(ctx context.Context, p auth.Principal) ([]dto.Project, error) {
	all, err := s.projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	chs, err := s.Channels(ctx, p, false)
	if err != nil {
		return nil, err
	}
	a := s.access(ctx, p)
	out := []dto.Project{}
	for _, pr := range all {
		if !a.Sees(pr.Name) {
			continue
		}
		d := dto.Project{Name: pr.Name, Title: pr.Title, CreatedAt: dto.TimeOf(pr.CreatedAt), Retention: RetentionDTO(pr)}
		for _, sc := range a.ProjectScopes(pr.Name) {
			if projects.GrantScopes.Has(sc) {
				d.Scopes = append(d.Scopes, dto.Scope(sc))
			}
		}
		for _, ch := range chs {
			if ch.Project() == pr.Name {
				d.Channels = append(d.Channels, ch.Stream())
				if ch.Gallery() {
					d.Galleries = append(d.Galleries, ch.Stream())
				}
			}
		}
		if a.ProjectScopes(pr.Name).Has(auth.ScopeRead) {
			sinks, err := s.projects.Sinks(ctx, pr.Name)
			if err != nil {
				return nil, err
			}
			for _, k := range sinks {
				d.Sinks = append(d.Sinks, k.Name)
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// RetentionDTO is a project's limits in seconds and bytes.
func RetentionDTO(p projects.Project) dto.ProjectRetention {
	r := p.Retention
	return dto.ProjectRetention{LinesMaxAgeS: int64(r.LinesMaxAge.Seconds()), LinesMaxBytes: r.LinesMaxBytes,
		MessagesMaxAgeS: int64(r.MessagesMaxAge.Seconds()), BlobsMaxBytes: r.BlobsMaxBytes}
}

type listProjectsOut struct {
	Projects []dto.Project `json:"projects"`
}

func (s *Service) toolListProjects(ctx context.Context, c *mcpserver.Call, _ struct{}) (string, listProjectsOut, error) {
	list, err := s.Projects(ctx, c.Principal)
	if err != nil {
		return "", listProjectsOut{}, err
	}
	out := listProjectsOut{Projects: list}
	if len(list) == 0 {
		return "You have no grant in any project yet; ask the admin for one.", out, nil
	}
	var b strings.Builder
	for _, pr := range list {
		fmt.Fprintf(&b, "%s", pr.Name)
		if pr.Title != "" {
			fmt.Fprintf(&b, " %q", pr.Title)
		}
		scopes := make([]string, len(pr.Scopes))
		for i, sc := range pr.Scopes {
			scopes[i] = string(sc)
		}
		if len(scopes) > 0 {
			fmt.Fprintf(&b, "; you may %s on the whole project", strings.Join(scopes, ", "))
		} else {
			b.WriteString("; you have grants on some channels only")
		}
		names := make([]string, len(pr.Channels))
		for i, ch := range pr.Channels {
			names[i] = "#" + strings.TrimPrefix(ch, "chan:")
		}
		fmt.Fprintf(&b, "; channels: %s", strings.Join(names, ", "))
		for _, g := range pr.Galleries {
			fmt.Fprintf(&b, "; gallery #%s (post screenshots there)", strings.TrimPrefix(g, "chan:"))
			ch, err := s.channel(ctx, g)
			if err != nil {
				continue
			}
			secs, err := s.GallerySections(ctx, ch)
			if err != nil {
				return "", listProjectsOut{}, err
			}
			var named []string
			for _, sec := range secs {
				if sec.Name != "" {
					named = append(named, sec.Name)
				}
			}
			if len(named) > 0 {
				fmt.Fprintf(&b, " with sections %s", strings.Join(named, ", "))
			}
		}
		if len(pr.Sinks) > 0 {
			fmt.Fprintf(&b, "; sinks: %s", strings.Join(pr.Sinks, ", "))
		}
		b.WriteByte('\n')
	}
	return b.String(), out, nil
}
