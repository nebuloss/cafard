package pages

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/web/view"
)

// The "+" of the left bar (DECISIONS 2026-11-20, "private channels"): one
// plain form page that makes a chat or a gallery, in a project
// (?project=lab, the "+" of its Chats and Galleries), or private in the
// reader's own space with the members it picks (the "+" of the direct
// messages). It works without the bundle.

// NewChannelHref is the form's address: private when project is "".
func NewChannelHref(project string, kind dto.ChannelKind) string {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if kind != "" {
		q.Set("kind", string(kind))
	}
	if len(q) == 0 {
		return "/channels/new"
	}
	return "/channels/new?" + q.Encode()
}

type newChannelData struct {
	Project, Label    string // "" for a private channel
	Private, Gallery  bool
	Name, Title       string
	Patterns          string
	Members           []memberOption
	Back, Placeholder string
}

type memberOption struct {
	Name, Kind string
	Checked    bool
}

func (s *Site) mountNewChannel(mux *http.ServeMux) {
	mux.HandleFunc("GET /channels/new", s.Auth(auth.ScopeMessage, s.newChannelPage))
	mux.Handle("POST /channels/new", s.Write(auth.ScopeMessage, s.newChannelPost))
}

// newChannelForm checks where the form makes a channel: a project p may
// make channels in, or p's own space. ok false has answered.
func (s *Site) newChannelForm(w http.ResponseWriter, r *http.Request, p auth.Principal, project string) (newChannelData, bool) {
	ctx := r.Context()
	data := newChannelData{Project: project, Private: project == "", Back: "/dm", Placeholder: "notes"}
	if data.Private {
		rows, err := s.chats.svc.PrivateCandidates(ctx, p)
		if err != nil {
			s.Oops(w, r, &p, err)
			return data, false
		}
		for _, row := range rows {
			data.Members = append(data.Members, memberOption{Name: row.Name, Kind: row.Kind})
		}
		return data, true
	}
	if projects.Personal(project) || !s.seesProject(ctx, p, project) {
		s.noProject(w, r, p, project)
		return data, false
	}
	if !s.d.Projects.Access(ctx, p).ProjectScopes(project).Has(auth.ScopeMessage) {
		s.Fail(w, r, &p, http.StatusForbidden, "Not allowed", "Making a channel in "+project+" needs message on the whole project; ask the admin for a grant.")
		return data, false
	}
	data.Back, data.Placeholder = view.ProjectHref(project), "run-42"
	if pr, err := s.d.Projects.Project(ctx, project); err == nil {
		data.Label = pr.Label()
	}
	return data, true
}

func (s *Site) newChannelPage(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	q := r.URL.Query()
	data, ok := s.newChannelForm(w, r, p, q.Get("project"))
	if !ok {
		return
	}
	data.Gallery = q.Get("kind") == string(dto.ChannelGallery)
	s.renderNewChannel(w, r, p, http.StatusOK, data, "")
}

func (s *Site) renderNewChannel(w http.ResponseWriter, r *http.Request, p auth.Principal, status int, data newChannelData, errText string) {
	title, at := "New private channel", Place{Nav: "direct", Item: "new"}
	if !data.Private {
		title, at = "New channel", Place{Project: data.Project}
	}
	l := s.shellFor(r, p, title, "newchannel", at)
	if data.Private {
		l.Kicker = "Direct messages"
	}
	l.Back = data.Back
	if errText != "" {
		l.Error = errText
	}
	s.Render(w, status, "new_channel", l, []string{"tpl-toast"}, data)
}

func (s *Site) newChannelPost(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		s.Fail(w, r, &p, http.StatusBadRequest, "Bad form", "The form could not be read.")
		return
	}
	data, ok := s.newChannelForm(w, r, p, r.PostForm.Get("project"))
	if !ok {
		return
	}
	data.Name, data.Title = strings.TrimSpace(r.PostForm.Get("name")), strings.TrimSpace(r.PostForm.Get("title"))
	data.Gallery = r.PostForm.Get("kind") == string(dto.ChannelGallery)
	data.Patterns = r.PostForm.Get("patterns")
	req := dto.ChannelCreate{Name: data.Name, Project: data.Project, Title: data.Title, Kind: dto.ChannelChat, Private: data.Private}
	if data.Gallery {
		req.Kind = dto.ChannelGallery
	}
	if data.Private {
		req.Members = r.PostForm["members"]
		for i := range data.Members {
			for _, m := range req.Members {
				data.Members[i].Checked = data.Members[i].Checked || m == data.Members[i].Name
			}
		}
	} else {
		req.Patterns = strings.FieldsFunc(data.Patterns, func(r rune) bool { return r == ',' || r == ' ' })
	}
	ch, err := s.chats.svc.CreateChannel(r.Context(), p, req)
	if err != nil {
		var apiErr *dto.APIError
		if errors.As(err, &apiErr) {
			s.renderNewChannel(w, r, p, dto.HTTPStatus(apiErr.Code), data, apiErr.Message)
			return
		}
		s.Oops(w, r, &p, err)
		return
	}
	http.Redirect(w, r, channelHref(ch), http.StatusSeeOther)
}

// channelHref is where a channel opens: a gallery's grid, or the chat.
func channelHref(ch messaging.Channel) string {
	if ch.Gallery() {
		return messaging.GalleryHref(ch.Name, nil)
	}
	return view.ChatHref(ch.Stream())
}
