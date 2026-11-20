package pages

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/assets"
	"github.com/nebuloss/cafard/internal/web/view"
)

// Home: the projects, with "Needs you" on top and, on wide screens, the
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
	s.AddTwin("tpl-member", "partials/member.html", MemberView{})
}

// Twins of each chat page.
var (
	homeTwins = []string{"tpl-message", "tpl-question", "tpl-choice", "tpl-event-row",
		"tpl-line", "tpl-image", "tpl-log-attachment", "tpl-attachment-chip", "tpl-upload-status", "tpl-needs-you-question", "tpl-gallery-ref", "tpl-toast"}
	chatTwins = []string{"tpl-member", "tpl-message", "tpl-question", "tpl-choice", "tpl-event-row",
		"tpl-line", "tpl-marker", "tpl-image", "tpl-log-attachment", "tpl-attachment-chip", "tpl-upload-status", "tpl-gallery-ref", "tpl-toast"}
	dropTwins   = []string{"tpl-image", "tpl-attachment-chip", "tpl-upload-status", "tpl-toast"}
	directTwins = []string{"tpl-toast"}
)

func (s *Site) mountHome(mux *http.ServeMux) {
	s.addChatTwins()
	mux.HandleFunc("GET /{$}", s.Auth(auth.ScopeRead, s.home))
	if s.chats != nil {
		s.mountChats(mux)
		s.mountGalleries(mux)
		s.mountNewChannel(mux)
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

// OverviewView is partials/overview.html.
type OverviewView struct {
	Recent []TimelineItem
	MCP    string
	Admin  bool
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
	NeedsYou NeedsYouView
	Welcome  string
	Muted    string // the muted chats, for "Needs you" in the bundle
	Overview OverviewView
	Drop     QuickDropView
	MCP      string
	Admin    bool
	CanSend  bool
}

// home is "Needs you", a welcome and the recent activity, beside the left
// bar (PLAN 9.6.3, DECISIONS "a Discord-like left bar"). It stays home
// with one project: Needs you lives here.
func (s *Site) home(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ctx := r.Context()
	data := homeData{MCP: view.MCPCommand(s.d.Server.BaseURL()), Admin: p.Has(auth.ScopeAdmin), CanSend: p.Has(auth.ScopeMessage)}
	l := s.shellFor(r, p, "Home", "home", Place{Nav: "home"})
	projects, direct := 0, 0
	if side := sideOf(l); side != nil {
		projects, direct = len(side.Projects), len(side.Direct)
	}
	data.Welcome = welcome(projects, direct, data.Admin)
	if s.chats == nil {
		s.Render(w, http.StatusOK, "home", l, homeTwins, data)
		return
	}
	chats, err := s.chats.svc.Chats(ctx, p, messaging.ChatsQuery{All: true})
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	var muted []string
	for _, ch := range chats {
		if ch.Muted {
			muted = append(muted, ch.ID)
		}
	}
	data.Muted = strings.Join(muted, " ")
	if data.NeedsYou, err = s.needsYou(ctx, p, chats); err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	o, err := s.chats.svc.Overview(ctx, p)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	now := s.d.Clock.Now()
	data.Overview = OverviewView{MCP: data.MCP, Admin: data.Admin,
		Recent: Items(reverse(o.Recent), ItemsOptions{Viewer: p.Name, Now: now, Back: "/", Next: "/", InList: true})}
	data.Drop = s.dropView(chats, r.URL.Query().Get("share"), p, "/")
	s.Render(w, http.StatusOK, "home", l, homeTwins, data)
}

// welcome is home's line under the greeting.
func welcome(projects, direct int, admin bool) string {
	switch {
	case projects == 0 && admin:
		return "No project yet. Make one below: a project holds chats and log sinks, keeps its own history and decides who sees it."
	case projects == 0:
		return "You have no grant in any project yet; ask the admin for one."
	}
	return "You are in " + plural(projects, "project", "projects") + " and " + plural(direct, "direct conversation", "direct conversations") +
		". Open one from the list; what needs you shows here."
}

func reverse[T any](s []T) []T {
	out := slices.Clone(s)
	slices.Reverse(out)
	return out
}

// plural is "1 chat" or "3 chats".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// needsYou is "Needs you": the open questions for p, then the direct
// conversations with unread messages.
func (s *Site) needsYou(ctx context.Context, p auth.Principal, chats []dto.Chat) (NeedsYouView, error) {
	var v NeedsYouView
	now := s.d.Clock.Now()
	for _, c := range chats {
		if c.Kind == dto.ChatDirect && c.Unread > 0 {
			v.Direct = append(v.Direct, ChatRow(c, now, ""))
		}
	}
	qs, err := s.chats.svc.QuestionsFor(ctx, p)
	if err != nil {
		return v, err
	}
	for _, it := range Items(qs, ItemsOptions{Viewer: p.Name, Now: now, Back: "/", Next: "/", InList: true}) {
		if it.Question != nil {
			v.Questions = append(v.Questions, *it.Question)
		}
	}
	return v, nil
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
func (s *Site) shellFor(r *http.Request, p auth.Principal, title, page string, at Place) assets.Layout {
	l := s.ShellAt(r, &p, title, page, at)
	l.Error = errorText(r.URL.Query().Get("error"))
	return l
}
