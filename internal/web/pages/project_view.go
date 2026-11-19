package pages

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/view"
)

// The project view (DECISIONS 2026-11-19, "projects first"): every page
// of a project lives under /projects/<name>, and the addresses chats and
// galleries had before redirect there, since agents' messages and gallery
// refs carry them.

func (s *Site) mountProjectView(mux *http.ServeMux) {
	mux.HandleFunc("GET /projects/{project}", s.Auth(auth.ScopeRead, s.projectEntry))
	mux.HandleFunc("GET /projects/{project}/logs", s.Auth(auth.ScopeRead, s.logsPage))
	mux.HandleFunc("GET /projects/{project}/logs/{sink}", s.Auth(auth.ScopeRead, s.logsPage))
	moved := func(pattern string, to func(r *http.Request) string) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			target := to(r)
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusPermanentRedirect)
		})
	}
	moved("GET /c/{project}/{name}", func(r *http.Request) string {
		return view.ChatHref("chan:" + r.PathValue("project") + "/" + r.PathValue("name"))
	})
	moved("GET /g/{project}", func(r *http.Request) string { return view.GalleriesHref(r.PathValue("project")) })
	moved("GET /g/{project}/{name}", func(r *http.Request) string {
		return messaging.GalleryHref(r.PathValue("project")+"/"+r.PathValue("name"), nil)
	})
	moved("GET /g/{project}/{name}/{seq}", func(r *http.Request) string {
		return messaging.GalleryHref(r.PathValue("project")+"/"+r.PathValue("name"), nil) + "/" + url.PathEscape(r.PathValue("seq"))
	})
}

// seesProject reports whether project exists and p may see it.
func (s *Site) seesProject(ctx context.Context, p auth.Principal, project string) bool {
	if !s.d.Projects.Access(ctx, p).Sees(project) {
		return false
	}
	_, err := s.d.Projects.Project(ctx, project)
	return err == nil
}

// noProject is the answer for a project p may not see, whether it exists
// or not.
func (s *Site) noProject(w http.ResponseWriter, r *http.Request, p auth.Principal, project string) {
	s.Fail(w, r, &p, http.StatusNotFound, "No such project", "There is no project "+strconv.Quote(project)+" you can see.")
}

// projectEntry opens a project on its most recently active chat, else
// #general, else a gallery, else its logs.
func (s *Site) projectEntry(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ctx := r.Context()
	project := r.PathValue("project")
	if !s.seesProject(ctx, p, project) {
		s.noProject(w, r, p, project)
		return
	}
	target := ""
	if s.chats != nil {
		chats, err := s.chats.svc.Chats(ctx, p, messaging.ChatsQuery{Project: project, All: true})
		if err != nil {
			s.Oops(w, r, &p, err)
			return
		}
		target = entryChat(chats)
	}
	if target == "" {
		target = view.LogsHref(project, "")
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// entryChat is the page a project opens on, or "" when it has no chat.
func entryChat(chats []dto.Chat) string {
	var latest, general, gallery *dto.Chat
	for i := range chats {
		c := &chats[i]
		switch {
		case c.Archived:
		case c.ChannelKind == dto.ChannelGallery:
			if gallery == nil {
				gallery = c
			}
		default:
			if c.Name == messaging.General {
				general = c
			}
			if c.LastActivity != nil && (latest == nil || c.LastActivity.After(latest.LastActivity.Time)) {
				latest = c
			}
		}
	}
	switch {
	case latest != nil:
		return view.ChatHref(latest.ID)
	case general != nil:
		return view.ChatHref(general.ID)
	case gallery != nil:
		return messaging.GalleryHref(strings.TrimPrefix(gallery.ID, "chan:"), nil)
	}
	return ""
}

// streamLogsHref is where a stream chip leads: its sink's logs in the
// project view, or Live for a stream outside projects.
func streamLogsHref(id string) string {
	if project := store.ProjectOf(id); project != "" {
		return view.LogsHref(project, store.LocalName(id))
	}
	return "/live?" + url.Values{"streams": {id}}.Encode()
}

type logsData struct {
	Side             SideView
	Project, Sink    string
	Heading, Caption string
	LiveHref         string // the same streams on Live, with every filter
	Cards            []StreamCardView
	Live             liveData
}

// logsTwins are the twins of a project's logs: Live's, the stream cards
// and the side list's rows.
var logsTwins = []string{"tpl-line", "tpl-marker", "tpl-attachment-chip", "tpl-upload-status", "tpl-stream-card", "tpl-stream-dot",
	"tpl-chat-row", "tpl-toast"}

// logsPage is the live logs of a project's sinks, or of one sink: its
// sources with their status, then the LogView of Live on them. It
// replaces Live and Sources inside a project; a viewer sees the streams
// its grants let it read and nothing else, as on Live.
func (s *Site) logsPage(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ctx := r.Context()
	project, sink := r.PathValue("project"), r.PathValue("sink")
	if !s.seesProject(ctx, p, project) || (sink != "" && auth.ValidateName(sink) != nil) {
		s.noProject(w, r, p, project)
		return
	}
	glob, key := "*:"+project+"/*", sideLogs
	if sink != "" {
		glob, key = "*:"+project+"/"+sink, sideLogsOf(sink)
	}
	q := r.URL.Query()
	q.Del("channel")
	q.Set("streams", glob)
	path := view.LogsHref(project, sink)
	live, status, err := s.logsView(ctx, p, q, path, r.URL.RequestURI(), true)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	side, err := s.projectSide(ctx, p, project, key)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data := logsData{Side: side, Project: project, Sink: sink, Live: live, Heading: "Logs",
		Caption: "Every sink of " + side.Label + ", live.", LiveHref: "/live?" + url.Values{"streams": {glob}}.Encode()}
	if sink != "" {
		data.Heading, data.Caption = sink, "The logs of sink "+project+"/"+sink+", live."
		for _, k := range side.Sinks {
			if k.Name == sink && k.Title != "" {
				data.Caption = k.Title + ": the logs of sink " + project + "/" + sink + ", live."
			}
		}
	}
	streams, err := view.VisibleStreams(ctx, s.d, p)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	now := s.d.Clock.Now()
	for _, st := range streams {
		if st.Project == project && (sink == "" || st.Sink == sink) {
			data.Cards = append(data.Cards, StreamCard(st, now))
		}
	}
	l := s.Shell(r, &p, data.Heading, "logs", "home")
	l.Side, l.Kicker, l.Back = true, side.Label, "/"
	s.Render(w, status, "logs", l, logsTwins, data)
}
