package pages

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/view"
)

// The project view (DECISIONS 2026-11-19, "projects first"): every page
// of a project lives under /projects/<name>, and the addresses chats and
// galleries had before redirect there, since agents' messages and gallery
// refs carry them.

func (s *Site) mountProjectView(mux *http.ServeMux) {
	mux.HandleFunc("GET /projects/{project}", s.Auth(auth.ScopeRead, s.projectEntry))
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

// projectEntry opens a project on its most recently active chat, else
// #general, else a gallery, else its logs.
func (s *Site) projectEntry(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ctx := r.Context()
	project := r.PathValue("project")
	if !s.d.Projects.Access(ctx, p).Sees(project) {
		s.Fail(w, r, &p, http.StatusNotFound, "No such project", "There is no project "+strconv.Quote(project)+" you can see.")
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
