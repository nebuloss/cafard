package pages

import (
	"context"
	"net/http"
	"net/url"
	"slices"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/assets"
	"github.com/nebuloss/cafard/internal/web/view"
)

// Home: the chat list with "Needs you" on top and, on wide screens, the
// overview pane (PLAN 9.6.3).

// chatSite is what the chat pages need beyond the Site.
type chatSite struct {
	svc    *messaging.Service
	shares *view.Shares
}

// SetChats gives the site the messaging service, which the chat pages
// read directly. Call it before Mount.
func (s *Site) SetChats(svc *messaging.Service, shares *view.Shares) {
	s.chats = &chatSite{svc: svc, shares: shares}
}

// The twins of the chat pages (CONTRACTS.md 6.2).
func (s *Site) addChatTwins() {
	s.AddTwin("tpl-chat-row", "partials/chat_row.html", ChatRowView{})
	s.AddTwin("tpl-message", "partials/message.html", MessageView{})
	s.AddTwin("tpl-question", "partials/question.html", QuestionView{})
	s.AddTwin("tpl-choice", "partials/choice", ChoiceView{})
	s.AddTwin("tpl-event-row", "partials/event_row.html", EventRowView{})
	s.AddTwin("tpl-image", "partials/image.html", ImageView{})
	s.AddTwin("tpl-log-attachment", "partials/log_attachment.html", LogAttachmentView{})
	s.AddTwin("tpl-attachment-chip", "partials/attachment_chip.html", AttachmentChipView{})
	s.AddTwin("tpl-upload-status", "partials/upload_status.html", UploadStatusView{})
	s.AddTwin("tpl-needs-you-question", "partials/question.html", QuestionView{InList: true})
	s.AddTwin("tpl-gallery-item", "partials/gallery_item.html", GalleryItemView{})
	s.AddTwin("tpl-gallery-ref", "partials/gallery_ref.html", GalleryRefView{})
}

// Twins of each chat page.
var (
	homeTwins = []string{"tpl-chat-row", "tpl-stream-dot", "tpl-message", "tpl-question", "tpl-choice", "tpl-event-row",
		"tpl-line", "tpl-image", "tpl-log-attachment", "tpl-attachment-chip", "tpl-upload-status", "tpl-needs-you-question", "tpl-gallery-ref", "tpl-toast"}
	chatTwins = []string{"tpl-chat-row", "tpl-stream-dot", "tpl-message", "tpl-question", "tpl-choice", "tpl-event-row",
		"tpl-line", "tpl-marker", "tpl-image", "tpl-log-attachment", "tpl-attachment-chip", "tpl-upload-status", "tpl-gallery-ref", "tpl-toast"}
	dropTwins = []string{"tpl-image", "tpl-attachment-chip", "tpl-upload-status", "tpl-toast"}
)

func (s *Site) mountHome(mux *http.ServeMux) {
	s.addChatTwins()
	mux.HandleFunc("GET /{$}", s.Auth(auth.ScopeRead, s.home))
	if s.chats != nil {
		s.mountChats(mux)
		s.mountGalleries(mux)
	}
}

// NeedsYouView is partials/needs_you.html: open questions for the viewer,
// then direct conversations with unread messages.
type NeedsYouView struct {
	Questions []QuestionView
	Direct    []ChatRowView
}

// Any reports whether the section has something to show.
func (n NeedsYouView) Any() bool { return len(n.Questions) > 0 || len(n.Direct) > 0 }

// ChatListView is partials/chat_list.html: the list on home and in the
// list pane of a chat, one section per project, Discord-like, then the
// direct conversations.
type ChatListView struct {
	NeedsYou   NeedsYouView
	Projects   []ProjectChatsView
	NoProjects string // what to say when there is no project to list
	Direct     []ChatRowView
	All        bool // unfollowed channels are listed
	ShowArch   bool // archived channels are listed
	AllHref    string
	ArchHref   string
	CanCreate  bool
}

// ProjectChatsView is one project of the chat list: its channels by
// state, and links to the logs of its sinks.
type ProjectChatsView struct {
	Name, Label string
	Channels    []ChatRowView // followed
	Unfollowed  []ChatRowView
	Archived    []ChatRowView
	Sinks       []SinkLinkView
	Galleries   string // the project's galleries page, when it has one the viewer reads
}

// SinkLinkView opens a sink's logs on Live.
type SinkLinkView struct{ Name, Href string }

// OverviewView is partials/overview.html.
type OverviewView struct {
	NeedsYou NeedsYouView
	Recent   []TimelineItem
	MCP      string
	Admin    bool
}

// QuickDropView is partials/quick_drop_sheet.html and the /drop form.
type QuickDropView struct {
	Chats    []ChatGroup
	ShareID  string
	Images   []ImageView
	Text     string
	Back     string
	MaxFiles int
}

// ChatOption is one choice of the chat picker.
type ChatOption struct {
	ID, Label string
	Selected  bool
}

type homeData struct {
	List     ChatListView
	Overview OverviewView
	Drop     QuickDropView
	Empty    bool // no message anywhere yet
	MCP      string
	Admin    bool
	CanSend  bool
}

func (s *Site) home(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	data := homeData{MCP: view.MCPCommand(s.d.Server.BaseURL()), Admin: p.Has(auth.ScopeAdmin), CanSend: p.Has(auth.ScopeMessage)}
	l := s.shellFor(r, p, "Chats", "home")
	if s.chats == nil {
		data.Empty = true
		s.Render(w, http.StatusOK, "home", l, homeTwins, data)
		return
	}
	q := r.URL.Query()
	list, chats, err := s.chatList(r.Context(), p, q.Get("all") == "1", q.Get("archived") == "1", "")
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data.List = list
	o, err := s.chats.svc.Overview(r.Context(), p)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	now := s.d.Clock.Now()
	data.Overview = OverviewView{NeedsYou: list.NeedsYou, MCP: data.MCP, Admin: data.Admin,
		Recent: Items(reverse(o.Recent), ItemsOptions{Viewer: p.Name, Now: now, Back: "/", Next: "/", InList: true})}
	data.Empty = len(o.Recent) == 0 && !list.NeedsYou.Any()
	data.Drop = s.dropView(chats, q.Get("share"), p, "/")
	s.Render(w, http.StatusOK, "home", l, homeTwins, data)
}

func reverse[T any](s []T) []T {
	out := slices.Clone(s)
	slices.Reverse(out)
	return out
}

// chatList builds the list; active marks the chat a page shows.
func (s *Site) chatList(ctx context.Context, p auth.Principal, all, archived bool, active string) (ChatListView, []dto.Chat, error) {
	svc := s.chats.svc
	chats, err := svc.Chats(ctx, p, messaging.ChatsQuery{All: all, Archived: archived})
	if err != nil {
		return ChatListView{}, nil, err
	}
	now := s.d.Clock.Now()
	v := ChatListView{All: all, ShowArch: archived, AllHref: "/?all=1", ArchHref: "/?archived=1", CanCreate: p.Has(auth.ScopeMessage)}
	if all {
		v.AllHref = "/"
	}
	if archived {
		v.ArchHref = "/"
	}
	projects, err := svc.Projects(ctx, p)
	if err != nil {
		return ChatListView{}, nil, err
	}
	v.NoProjects = "You have no grant in any project yet; ask the admin for one."
	if p.Has(auth.ScopeAdmin) {
		v.NoProjects = "No project yet: make one on the Projects page of the admin interface."
	}
	at := map[string]int{}
	for _, pr := range projects {
		at[pr.Name] = len(v.Projects)
		g := ProjectChatsView{Name: pr.Name, Label: pr.Name}
		if pr.Title != "" {
			g.Label = pr.Title
		}
		for _, k := range pr.Sinks {
			g.Sinks = append(g.Sinks, SinkLinkView{Name: k, Href: "/live?" + url.Values{"streams": {"*:" + pr.Name + "/" + k}}.Encode()})
		}
		if len(pr.Galleries) > 0 {
			g.Galleries = view.GalleriesHref(pr.Name)
		}
		v.Projects = append(v.Projects, g)
	}
	for _, c := range chats {
		row := ChatRow(c, now, active)
		if c.Kind == dto.ChatDirect {
			v.Direct = append(v.Direct, row)
			if c.Unread > 0 {
				v.NeedsYou.Direct = append(v.NeedsYou.Direct, row)
			}
			continue
		}
		i, ok := at[c.Project]
		if !ok {
			continue
		}
		g := &v.Projects[i]
		switch {
		case c.Archived:
			g.Archived = append(g.Archived, row)
		case !c.Followed:
			g.Unfollowed = append(g.Unfollowed, row)
		default:
			g.Channels = append(g.Channels, row)
		}
	}
	qs, err := svc.QuestionsFor(ctx, p)
	if err != nil {
		return v, nil, err
	}
	for _, it := range Items(qs, ItemsOptions{Viewer: p.Name, Now: now, Back: "/", Next: "/", InList: true}) {
		if it.Question != nil {
			v.NeedsYou.Questions = append(v.NeedsYou.Questions, *it.Question)
		}
	}
	return v, chats, nil
}

// errorText is the banner for the ?error= code a no-JS post came back
// with. The JSON answer carries the server's full sentence; a redirect
// only carries the code, so each gets a sentence here.
func errorText(code string) string {
	switch code {
	case "":
		return ""
	case dto.CodeRateLimited:
		return "You are sending messages too fast. Wait a few seconds and send again."
	case dto.CodeTooLarge:
		return "That was too big to send: a message holds 32 KiB of text and at most 10 photos of 20 MiB each."
	case dto.CodeBadRequest:
		return "The message was not sent: it needs text, a photo or a log attachment, and an answer must be one of the question's choices."
	case dto.CodeNotFound:
		return "The message was not sent: the message it answers, or the files it shares, are gone."
	case dto.CodeUnknownChannel:
		return "The message was not sent: that channel does not exist."
	case dto.CodeForbidden:
		return "The message was not sent: this session may read but not send (it lacks the message scope)."
	case dto.CodeCrossOrigin:
		return "The message was not sent: the form came from another site."
	}
	// Not echoed: the code comes from the URL.
	return "The message was not sent."
}

// shellFor is the layout of a chat page, with the banner of a failed
// no-JS post.
func (s *Site) shellFor(r *http.Request, p auth.Principal, title, page string) assets.Layout {
	l := s.Shell(r, &p, title, page, "chats")
	l.Error = errorText(r.URL.Query().Get("error"))
	return l
}
