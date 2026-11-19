package pages

import (
	"context"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/view"
)

// The side list of the project view (partials/side_nav.html): a sidebar
// from 840 px, a drawer below. A project's chats, galleries, log sinks and
// settings, or the direct conversations, with the switcher on top.

// SideView is partials/side_nav.html.
type SideView struct {
	Project       string // "" on the direct conversations
	Label         string
	Switch        []SwitchLink
	DirectUnread  string
	Chats         []ChatRowView // followed chat channels
	Unfollowed    []ChatRowView
	Archived      []ChatRowView
	Galleries     []ChatRowView
	GalleriesHref string
	GalleriesOn   bool
	Direct        []ChatRowView
	Sinks         []SinkRowView
	Logs          string // the logs overview; "" when the viewer may not read the project's logs
	LogsOn        bool
	Settings      string // the admin's
	SettingsOn    bool
	CanCreate     bool // "New channel" (bundle)
}

// SwitchLink is a project in the switcher.
type SwitchLink struct {
	Name, Label, Initial, Href, Unread string
	Current                            bool
}

// SinkRowView is a sink in the side list: a dot per stream it holds, and
// the time of its last line. Status sums the dots up: down when one is,
// else connected when one is, else idle; "" before any line.
type SinkRowView struct {
	Ref, Name, Title, Href string
	Status, Time, ISO      string
	Dots                   []StreamDotView
	Active                 bool
}

// The keys of what a page of the project view shows, beside chat ids.
const (
	sideLogs      = "logs"
	sideSettings  = "settings"
	sideGalleries = "galleries"
)

// sideLogsOf is the side key of one sink's logs.
func sideLogsOf(sink string) string { return sideLogs + "/" + sink }

// visibleProjects are the projects p sees, as the API lists them. Without
// messaging they carry no channels.
func (s *Site) visibleProjects(ctx context.Context, p auth.Principal) ([]dto.Project, error) {
	if s.chats != nil {
		return s.chats.svc.Projects(ctx, p)
	}
	all, err := s.d.Projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	acc := s.d.Projects.Access(ctx, p)
	var out []dto.Project
	for _, pr := range all {
		if !acc.Sees(pr.Name) {
			continue
		}
		d := dto.Project{Name: pr.Name, Title: pr.Title, CreatedAt: dto.TimeOf(pr.CreatedAt)}
		if acc.ProjectScopes(pr.Name).Has(auth.ScopeRead) {
			sinks, err := s.d.Projects.Sinks(ctx, pr.Name)
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

// projectUnread sums the unread messages and open questions of each
// project's chats; src/components/ProjectCards.ts does the same with the
// counts event.
func projectUnread(c dto.Counts) (unread, questions map[string]int) {
	unread, questions = map[string]int{}, map[string]int{}
	for _, ch := range c.Chats {
		name, ok := strings.CutPrefix(ch.ID, "chan:")
		if !ok {
			continue
		}
		project, _, _ := strings.Cut(name, "/")
		unread[project] += ch.Unread
		questions[project] += ch.OpenQuestions
	}
	return unread, questions
}

func projectLabel(pr dto.Project) string {
	if pr.Title != "" {
		return pr.Title
	}
	return pr.Name
}

// switchLinks is the project switcher; current is "" on the direct
// conversations.
func switchLinks(list []dto.Project, unread map[string]int, current string) []SwitchLink {
	out := make([]SwitchLink, 0, len(list))
	for _, pr := range list {
		label := projectLabel(pr)
		out = append(out, SwitchLink{Name: pr.Name, Label: label, Initial: initial(label), Href: view.ProjectHref(pr.Name),
			Unread: count(unread[pr.Name]), Current: pr.Name == current})
	}
	return out
}

// projectSide is the side list of project, with active marked: a chat id
// or one of the side keys.
func (s *Site) projectSide(ctx context.Context, p auth.Principal, project, active string) (SideView, error) {
	list, err := s.visibleProjects(ctx, p)
	if err != nil {
		return SideView{}, err
	}
	c := view.Counts(ctx, s.d, p)
	unread, _ := projectUnread(c)
	v := SideView{Project: project, Label: project, Switch: switchLinks(list, unread, project), DirectUnread: count(c.UnreadDirect),
		GalleriesHref: view.GalleriesHref(project), GalleriesOn: active == sideGalleries}
	var pr *dto.Project
	for i := range list {
		if list[i].Name == project {
			pr = &list[i]
		}
	}
	if pr == nil {
		return v, nil
	}
	v.Label = projectLabel(*pr)
	acc := s.d.Projects.Access(ctx, p)
	v.CanCreate = s.chats != nil && p.Has(auth.ScopeMessage) && acc.ProjectScopes(project).Has(auth.ScopeMessage)
	if p.Has(auth.ScopeAdmin) {
		v.Settings, v.SettingsOn = view.SettingsHref(project), active == sideSettings
	}
	if s.chats != nil {
		chats, err := s.chats.svc.Chats(ctx, p, messaging.ChatsQuery{Project: project, All: true, Archived: true})
		if err != nil {
			return v, err
		}
		now := s.d.Clock.Now()
		for _, ch := range chats {
			row := ChatRow(ch, now, active)
			switch {
			case ch.Archived:
				v.Archived = append(v.Archived, row)
			case ch.ChannelKind == dto.ChannelGallery:
				if row.Items, err = s.galleryCount(ctx, p, ch); err != nil {
					return v, err
				}
				v.Galleries = append(v.Galleries, row)
			case !ch.Followed:
				v.Unfollowed = append(v.Unfollowed, row)
			default:
				v.Chats = append(v.Chats, row)
			}
		}
	}
	if len(pr.Sinks) > 0 {
		v.Logs, v.LogsOn = view.LogsHref(project, ""), active == sideLogs
		if v.Sinks, err = s.sinkRows(ctx, p, *pr, active); err != nil {
			return v, err
		}
	}
	return v, nil
}

// galleryCount is "N images" for a gallery's row.
func (s *Site) galleryCount(ctx context.Context, p auth.Principal, c dto.Chat) (string, error) {
	ch, err := s.chats.svc.Gallery(ctx, p, strings.TrimPrefix(c.ID, "chan:"))
	if err != nil {
		return "", nil
	}
	page, err := s.chats.svc.GalleryPage(ctx, ch, nil, 0, 1)
	if err != nil {
		return "", err
	}
	n := 0
	for _, sec := range page.Sections {
		n += sec.Items
	}
	return ImageCount(n), nil
}

// sinkRows are a project's sinks with the status of their streams.
func (s *Site) sinkRows(ctx context.Context, p auth.Principal, pr dto.Project, active string) ([]SinkRowView, error) {
	streams, err := view.VisibleStreams(ctx, s.d, p)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	if sinks, err := s.d.Projects.Sinks(ctx, pr.Name); err == nil {
		for _, k := range sinks {
			titles[k.Name] = k.Title
		}
	}
	now := s.d.Clock.Now()
	out := make([]SinkRowView, 0, len(pr.Sinks))
	for _, name := range pr.Sinks {
		row := SinkRowView{Ref: pr.Name + "/" + name, Name: name, Title: titles[name], Href: view.LogsHref(pr.Name, name),
			Active: active == sideLogsOf(name)}
		var last *dto.Time
		for _, st := range streams {
			if st.Project != pr.Name || st.Sink != name {
				continue
			}
			row.Dots = append(row.Dots, StreamDotView{Status: string(st.Status), Title: st.ID + ": " + string(st.Status)})
			if st.LastActivity != nil && (last == nil || st.LastActivity.After(last.Time)) {
				last = st.LastActivity
			}
		}
		row.Status = sinkStatus(row.Dots)
		if last != nil {
			row.Time, row.ISO = MessageTime(last.Time, now), iso(last.Time)
		}
		out = append(out, row)
	}
	return out, nil
}

// sinkStatus sums up a sink's streams; src/components/SideNav.ts
// sinkStatus matches it.
func sinkStatus(dots []StreamDotView) string {
	status := ""
	for _, d := range dots {
		switch {
		case d.Status == string(dto.StreamDown):
			return d.Status
		case d.Status == string(dto.StreamConnected):
			status = d.Status
		case status == "":
			status = d.Status
		}
	}
	return status
}

// directSide is the side list of the direct conversations.
func (s *Site) directSide(ctx context.Context, p auth.Principal, active string) (SideView, error) {
	list, err := s.visibleProjects(ctx, p)
	if err != nil {
		return SideView{}, err
	}
	c := view.Counts(ctx, s.d, p)
	unread, _ := projectUnread(c)
	v := SideView{Label: "Direct messages", Switch: switchLinks(list, unread, ""), DirectUnread: count(c.UnreadDirect)}
	if s.chats == nil {
		return v, nil
	}
	chats, err := s.chats.svc.Chats(ctx, p, messaging.ChatsQuery{})
	if err != nil {
		return v, err
	}
	now := s.d.Clock.Now()
	for _, ch := range chats {
		if ch.Kind == dto.ChatDirect {
			v.Direct = append(v.Direct, ChatRow(ch, now, active))
		}
	}
	return v, nil
}
