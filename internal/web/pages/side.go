package pages

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/assets"
	"github.com/nebuloss/cafard/internal/web/view"
)

// The left bar (partials/side_nav.html), Discord-like: Home, the direct
// messages, then every project the reader sees as a tree that unfolds in
// place (its chats, galleries, log sinks and, for the admin, settings),
// and the reader's own corner at the bottom. A column from 840 px, the
// drawer below. Every signed-in page has it.

// Place is where a page sits in the left bar.
type Place struct {
	Nav     string // "home", "direct" or "admin": the bar's entry of the page
	Project string // the project unfolded and marked as the current one
	Item    string // a chat id, or a side key (sideLogs, sideLogsOf, sideGalleries, sideSettings)
}

// SideView is partials/side_nav.html (with the layout around it).
type SideView struct {
	Home, AdminOn bool
	Questions     int  // open questions for the reader, on Home
	DirectOpen    bool // the page is a direct conversation, or /dm
	DirectList    bool // the page is /dm
	DirectUnread  int
	Direct        []ChatRowView
	Projects      []SideProject
	NoProjects    string
	SourcesDown   bool // the dot on Admin
	CanCreate     bool // some project offers "New channel"
}

// SideProject is one project of the left bar.
type SideProject struct {
	Name, Label, Initials string
	Unread, Questions     string // "" when zero
	Current               bool   // the page's project, unfolded
	Chats, Unfollowed     []ChatRowView
	Archived, Galleries   []ChatRowView
	GalleriesHref         string
	GalleriesOn           bool
	Sinks                 []SinkRowView
	Logs, Settings        string // "" when the reader may not read the logs, or is not the admin
	LogsOn, SettingsOn    bool
	CanCreate             bool
}

// SinkRowView is a sink in the left bar: a dot per stream it holds, and
// the time of its last line. Status sums the dots up: down when one is,
// else connected when one is, else idle; "" before any line.
type SinkRowView struct {
	Ref, Name, Title, Href string
	Status, Time, ISO      string
	Dots                   []StreamDotView
	Active                 bool
}

// The side keys of what a project page shows, beside chat ids.
const (
	sideLogs      = "logs"
	sideSettings  = "settings"
	sideGalleries = "galleries"
)

// sideLogsOf is the side key of one sink's logs.
func sideLogsOf(sink string) string { return sideLogs + "/" + sink }

// Label is the title of a project of the bar, or its name.
func (v SideView) Label(project string) string {
	for _, pr := range v.Projects {
		if pr.Name == project {
			return pr.Label
		}
	}
	return project
}

// Sink is the bar's row of a sink of project, if it lists it.
func (v SideView) Sink(project, sink string) (SinkRowView, bool) {
	for _, pr := range v.Projects {
		for _, k := range pr.Sinks {
			if pr.Name == project && k.Name == sink {
				return k, true
			}
		}
	}
	return SinkRowView{}, false
}

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
// project's chats; src/components/SideNav.ts projectTotals does the same
// with the counts event.
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

// initials are a project's round icon: the first letter of its first two
// words, "LB" for "Lab bench".
func initials(label string) string {
	var out []rune
	for _, w := range strings.FieldsFunc(label, func(r rune) bool { return unicode.IsSpace(r) || r == '-' || r == '_' || r == '.' }) {
		r, _ := utf8.DecodeRuneInString(w)
		out = append(out, unicode.ToUpper(r))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// side is the left bar of a page at a place, with the reader's badges c.
func (s *Site) side(ctx context.Context, p auth.Principal, at Place, c dto.Counts) (SideView, error) {
	list, err := s.visibleProjects(ctx, p)
	if err != nil {
		return SideView{}, err
	}
	unread, questions := projectUnread(c)
	v := SideView{Home: at.Nav == "home", AdminOn: at.Nav == "admin", Questions: c.OpenQuestions,
		DirectOpen: at.Nav == "direct", DirectList: at.Nav == "direct" && at.Item == "", DirectUnread: c.UnreadDirect, SourcesDown: c.SourcesDown > 0,
		NoProjects: "You have no grant in any project yet; ask the admin for one."}
	if p.Has(auth.ScopeAdmin) {
		v.NoProjects = "No project yet: make one from Home."
	}
	var chats []dto.Chat
	if s.chats != nil {
		if chats, err = s.chats.svc.Chats(ctx, p, messaging.ChatsQuery{All: true, Archived: true}); err != nil {
			return v, err
		}
	}
	now := s.d.Clock.Now()
	var streams []dto.Stream
	for _, pr := range list {
		if len(pr.Sinks) > 0 {
			if streams, err = view.VisibleStreams(ctx, s.d, p); err != nil {
				return v, err
			}
			break
		}
	}
	acc := s.d.Projects.Access(ctx, p)
	byName := map[string]int{}
	for _, pr := range list {
		label := projectLabel(pr)
		here := pr.Name == at.Project
		item := ""
		if here {
			item = at.Item
		}
		sp := SideProject{Name: pr.Name, Label: label, Initials: initials(label), Unread: count(unread[pr.Name]),
			Questions: count(questions[pr.Name]), Current: here, GalleriesHref: view.GalleriesHref(pr.Name),
			GalleriesOn: item == sideGalleries,
			CanCreate:   s.chats != nil && p.Has(auth.ScopeMessage) && acc.ProjectScopes(pr.Name).Has(auth.ScopeMessage)}
		if p.Has(auth.ScopeAdmin) {
			sp.Settings, sp.SettingsOn = view.SettingsHref(pr.Name), item == sideSettings
		}
		if len(pr.Sinks) > 0 {
			sp.Logs, sp.LogsOn = view.LogsHref(pr.Name, ""), item == sideLogs
			sp.Sinks = s.sinkRows(ctx, pr, streams, item, now)
		}
		v.CanCreate = v.CanCreate || sp.CanCreate
		byName[pr.Name] = len(v.Projects)
		v.Projects = append(v.Projects, sp)
	}
	var seen map[string]*time.Time
	for _, ch := range chats {
		row := ChatRow(ch, now, at.Item)
		if ch.Kind == dto.ChatDirect {
			if seen == nil {
				if seen, err = s.lastSeen(ctx); err != nil {
					return v, err
				}
			}
			row.Presence = "offline"
			if s.presence.Online(ch.Name, seen[ch.Name], now) {
				row.Presence = "online"
			}
			v.Direct = append(v.Direct, row)
			continue
		}
		i, ok := byName[ch.Project]
		if !ok {
			continue
		}
		sp := &v.Projects[i]
		switch {
		case ch.Archived:
			sp.Archived = append(sp.Archived, row)
		case ch.ChannelKind == dto.ChannelGallery:
			if row.Items, err = s.galleryCount(ctx, p, ch); err != nil {
				return v, err
			}
			sp.Galleries = append(sp.Galleries, row)
		case !ch.Followed:
			sp.Unfollowed = append(sp.Unfollowed, row)
		default:
			sp.Chats = append(sp.Chats, row)
		}
	}
	return v, nil
}

// lastSeen is every principal's last request.
func (s *Site) lastSeen(ctx context.Context) (map[string]*time.Time, error) {
	rows, err := s.d.Tokens.Principals(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]*time.Time, len(rows))
	for _, r := range rows {
		out[r.Name] = r.LastSeen
	}
	return out, nil
}

// galleryCount is "N images" for a gallery's row.
func (s *Site) galleryCount(ctx context.Context, p auth.Principal, c dto.Chat) (string, error) {
	ch, err := s.chats.svc.Gallery(ctx, p, strings.TrimPrefix(c.ID, "chan:"))
	if err != nil {
		return "", err
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
func (s *Site) sinkRows(ctx context.Context, pr dto.Project, streams []dto.Stream, item string, now time.Time) []SinkRowView {
	titles := map[string]string{}
	if sinks, err := s.d.Projects.Sinks(ctx, pr.Name); err == nil {
		for _, k := range sinks {
			titles[k.Name] = k.Title
		}
	}
	out := make([]SinkRowView, 0, len(pr.Sinks))
	for _, name := range pr.Sinks {
		row := SinkRowView{Ref: pr.Name + "/" + name, Name: name, Title: titles[name], Href: view.LogsHref(pr.Name, name),
			Active: item == sideLogsOf(name)}
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
	return out
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

// sideOf is the left bar a layout carries, or nil.
func sideOf(l assets.Layout) *SideView {
	v, _ := l.Side.(*SideView)
	return v
}
