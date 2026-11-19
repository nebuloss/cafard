package pages

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/web/assets"
	"github.com/nebuloss/cafard/internal/web/view"
)

// A conversation, /c/{name} and /dm/{peer} (PLAN 9.6.3). The
// server renders the latest 50 rows, the composer, and for a bound
// channel the logs of its streams; the bundle follows from there.

// ChatPageSize is how many timeline rows a chat page renders.
const ChatPageSize = 50

// Lines a bound channel's logs pane renders: fewer beside the chat, the
// Live page's 500 in the logs view.
const (
	paneLines = 200
	viewLines = LiveLines
)

func (s *Site) mountChats(mux *http.ServeMux) {
	mux.HandleFunc("GET /c/{project}/{name}", s.Auth(auth.ScopeRead, s.channelPage))
	mux.HandleFunc("GET /dm/{peer}", s.Auth(auth.ScopeRead, s.directPage))
	mux.HandleFunc("GET /drop", s.Auth(auth.ScopeMessage, s.drop))
	mux.Handle("POST /share", s.Write(auth.ScopeMessage, s.share))
	mux.HandleFunc("GET /seen", s.Auth(auth.ScopeMessage, s.seen))
}

// HeaderView is partials/channel_header.html.
type HeaderView struct {
	ID, Name, Title, Topic string
	Direct                 bool
	Streams                []StreamChip
	Claims                 []string
	ChatHref, LogsHref     string
	Logs                   bool // the logs view is the one shown
	Bound                  bool
	Manage                 bool // channel settings are offered
	PeerKind               string
}

// StreamChip is a bound stream in the header: it opens Live on it.
type StreamChip struct {
	ID, Status, Href string
}

// FilterChip is one chip of the conversation filter.
type FilterChip struct {
	Value, Label, Href string
	Active             bool
}

// ComposerView is partials/composer.html.
type ComposerView struct {
	Chat, Placeholder string
	CanSend           bool
	MaxFiles          int
	Bound             bool // offers "Attach logs"
	Streams           []string
}

// LogsPaneView is partials/logs_pane.html.
type LogsPaneView struct {
	Items      []LogItem
	Query      string // the LogView's /api/events and /api/entries query
	NextSeq    int64
	Action     string // the no-JS filter form posts here
	Re         string
	Severities []sevOption
	Showing    string
	Streams    []string
	Error      string
}

// DialogView is partials/channel_dialog.html.
type DialogView struct {
	Name, Title, Topic string
	Patterns           []string
	Surfaces           []sevOption
	Muted, Followed    bool
	Pinned, Archived   bool
	CanManage, Config  bool
}

// BannerView is the sticky "N questions waiting" banner.
type BannerView struct {
	Text, Href string
}

type chatData struct {
	Header    HeaderView
	List      ChatListView
	Items     []TimelineItem
	Older     string
	Newer     string // back to the latest page from an older one
	Filters   []FilterChip
	Banner    *BannerView
	Composer  ComposerView
	Logs      *LogsPaneView
	Dialog    *DialogView
	View      string // "chat" or "logs"
	Chat      string
	Surface   string
	NextSeq   int64
	Follow    bool
	Filter    string
	Empty     bool
	SeenSrc   string // the no-JS read beacon
	Shortcuts bool
}

var filterChips = []struct{ value, label string }{
	{messaging.FilterAll, "All"}, {messaging.FilterQuestions, "Questions"}, {messaging.FilterEvents, "Events"},
	{messaging.FilterMine, "Mine"}, {messaging.FilterImages, "With images"},
}

var surfaceLabels = []sevOption{
	{string(dto.SurfaceNone), "Nothing from the streams", false},
	{string(dto.SurfaceMarkers), "Markers", false},
	{string(dto.SurfaceErrors), "Markers and errors", false},
}

func (s *Site) channelPage(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ctx := r.Context()
	ch, err := s.chats.svc.Channel(ctx, p, r.PathValue("project")+"/"+r.PathValue("name"))
	var apiErr *dto.APIError
	if errors.As(err, &apiErr) {
		s.Fail(w, r, &p, http.StatusNotFound, "No such channel", apiErr.Message)
		return
	}
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	chat, err := s.chats.svc.ChannelChat(ctx, p, ch)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	s.chatPage(w, r, p, ch.Stream(), chat, &ch)
}

func (s *Site) directPage(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	peer := r.PathValue("peer")
	if peer == p.Name {
		s.Fail(w, r, &p, http.StatusNotFound, "No such conversation", "A direct conversation needs another principal.")
		return
	}
	if _, err := s.d.Tokens.Principal(r.Context(), peer); err != nil {
		s.Fail(w, r, &p, http.StatusNotFound, "No such conversation", "There is no principal "+strconv.Quote(peer)+".")
		return
	}
	chat, err := s.chats.svc.DirectChat(r.Context(), p, peer)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	s.chatPage(w, r, p, messaging.DMStream(p.Name, peer), chat, nil)
}

func (s *Site) chatPage(w http.ResponseWriter, r *http.Request, p auth.Principal, stream string, chat dto.Chat, ch *messaging.Channel) {
	ctx := r.Context()
	svc := s.chats.svc
	q := r.URL.Query()
	base := view.ChatHref(chat.ID)
	f := q.Get("f")
	if !messaging.ValidFilter(f) || f == messaging.FilterAll {
		f = ""
	}
	data := chatData{Chat: chat.ID, Surface: string(chat.Surface), View: "chat", Filter: f, Shortcuts: true,
		Composer: ComposerView{Chat: chat.ID, Placeholder: "Message " + ChatName(chat), CanSend: p.Has(auth.ScopeMessage),
			MaxFiles: svc.Blobs().Config().MaxFiles}}
	bound := ch != nil && len(ch.Patterns) > 0
	if bound && q.Get("view") == "logs" {
		data.View = "logs"
	}

	last, err := s.d.Store.LastSeq(ctx)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	before := last + 1
	if v, err := strconv.ParseInt(q.Get("before_seq"), 10, 64); err == nil && v > 0 {
		before = v
		data.Newer = withQuery(base, url.Values{"f": {f}})
	}
	page, err := svc.FilteredTimeline(ctx, p, stream, messaging.TimelineQuery{BeforeSeq: before, Limit: ChatPageSize}, f)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	entries, err := svc.TimelineDTO(ctx, p, page)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	now := s.d.Clock.Now()
	data.Items = Items(entries.Items, ItemsOptions{Viewer: p.Name, Now: now})
	data.Empty = len(data.Items) == 0
	if page.HasMore && len(page.Rows) > 0 {
		data.Older = withQuery(base, url.Values{"before_seq": {strconv.FormatInt(page.Rows[0].Seq, 10)}, "f": {f}})
	}
	data.Follow = data.Newer == ""
	data.NextSeq = last + 1
	for _, c := range filterChips {
		if c.value == messaging.FilterEvents && !bound {
			continue
		}
		href := withQuery(base, url.Values{"f": {strings.TrimPrefix(c.value, messaging.FilterAll)}})
		data.Filters = append(data.Filters, FilterChip{Value: c.value, Label: c.label, Href: href,
			Active: c.value == f || (f == "" && c.value == messaging.FilterAll)})
	}

	if err := s.chatHeader(ctx, &data, p, chat, ch, base); err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	if data.List, _, err = s.chatList(ctx, p, false, false, chat.ID); err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data.Banner = banner(data.List.NeedsYou.Questions, chat.ID, data.Items, base)
	if bound {
		data.Logs = s.logsPane(ctx, r, p, ch, base, data.View == "logs")
	}
	if !p.Has(auth.ScopeMessage) {
		data.Composer.CanSend = false
	}
	s.ackShown(ctx, p, &data, entries.Items)

	title := ChatName(chat)
	l := s.shellFor(r, p, title, "chat")
	l.Back = "/"
	s.Render(w, http.StatusOK, "chat", l, chatTwins, data)
}

func withQuery(base string, v url.Values) string {
	for k, vs := range v {
		if len(vs) == 0 || vs[0] == "" {
			delete(v, k)
		}
	}
	if len(v) == 0 {
		return base
	}
	return base + "?" + v.Encode()
}

func (s *Site) chatHeader(ctx context.Context, data *chatData, p auth.Principal, chat dto.Chat, ch *messaging.Channel, base string) error {
	h := HeaderView{ID: chat.ID, Name: ChatName(chat), Title: chat.Title, Topic: chat.Topic, Direct: ch == nil,
		ChatHref: base, LogsHref: withQuery(base, url.Values{"view": {"logs"}}), Logs: data.View == "logs"}
	if chat.Peer != nil {
		h.PeerKind = string(chat.Peer.Kind)
	}
	if ch != nil {
		h.Bound = len(ch.Patterns) > 0
		h.Manage = p.Has(auth.ScopeMessage)
		claims, err := s.chats.svc.ActiveClaims(ctx)
		if err != nil {
			return err
		}
		for _, st := range chat.Streams {
			h.Streams = append(h.Streams, StreamChip{ID: st.ID, Status: string(st.Status), Href: "/live?" + url.Values{"streams": {st.ID}}.Encode()})
			if c, ok := claims[st.ID]; ok {
				h.Claims = append(h.Claims, c.Principal+" holds "+st.ID+" until "+c.ExpiresAt.UTC().Format("15:04")+" UTC")
			}
		}
		d := &DialogView{Name: ch.Name, Title: ch.Title, Topic: ch.Topic, Patterns: ch.Patterns,
			Muted: chat.Muted, Followed: chat.Followed, Pinned: chat.Pinned, Archived: chat.Archived,
			CanManage: chat.CanManage, Config: ch.Source == dto.SourceConfig}
		for _, o := range surfaceLabels {
			o.Selected = o.Value == string(ch.Surface)
			d.Surfaces = append(d.Surfaces, o)
		}
		data.Dialog = d
		data.Composer.Bound = h.Bound
		for _, st := range chat.Streams {
			data.Composer.Streams = append(data.Composer.Streams, st.ID)
		}
	}
	data.Header = h
	return nil
}

// banner is the sticky line naming the open questions for the viewer in
// this chat: it scrolls to the first one, or filters to them when it is
// not on the page.
func banner(qs []QuestionView, chat string, items []TimelineItem, base string) *BannerView {
	var mine []QuestionView
	for _, q := range qs {
		if q.Chat == chat {
			mine = append(mine, q)
		}
	}
	if len(mine) == 0 {
		return nil
	}
	b := &BannerView{Text: "1 question waiting for you", Href: withQuery(base, url.Values{"f": {messaging.FilterQuestions}})}
	if len(mine) > 1 {
		b.Text = strconv.Itoa(len(mine)) + " questions waiting for you"
	}
	oldest := mine[len(mine)-1]
	for _, it := range items {
		if it.Question != nil && it.Question.Seq == oldest.Seq {
			b.Href = "#" + oldest.ID
		}
	}
	return b
}

func (s *Site) logsPane(ctx context.Context, r *http.Request, p auth.Principal, ch *messaging.Channel, base string, full bool) *LogsPaneView {
	q := r.URL.Query()
	pane := &LogsPaneView{Action: base, Re: q.Get("re"), Streams: ch.Patterns}
	f, apiErr := view.ParseLogFilter(url.Values{"channel": {ch.Name}, "sev": {q.Get("sev")}, "re": {q.Get("re")}})
	pane.Severities, _ = sevOptions(q.Get("sev"), f)
	if apiErr != nil {
		pane.Error = apiErr.Message
		return pane
	}
	limit := paneLines
	if full {
		limit = viewLines
	}
	pane.Query = f.URL()
	patterns, apiErr := f.Patterns(ctx, s.d, p)
	if apiErr != nil {
		pane.Error = apiErr.Message
		return pane
	}
	ids, err := view.StreamIDs(ctx, s.d.Store, s.d.Projects.Access(ctx, p), patterns)
	if err != nil {
		pane.Error = "The lines could not be read."
		return pane
	}
	pg, err := view.ReadLogs(ctx, s.d.Store, f, ids, limit)
	if err != nil {
		pane.Error = "The lines could not be read."
		return pane
	}
	pane.Items, pane.NextSeq = LiveItems(pg.Entries, ""), pg.NextSeq
	pane.Showing = "the last " + strconv.Itoa(limit) + " lines"
	return pane
}

// ackShown acks the messages the page shows when no bundle will: the
// binary has none, so the render is the reading. With a bundle, the page
// carries a beacon in <noscript>, which only a browser without scripts
// loads; scripts ack what stays visible for a second instead.
func (s *Site) ackShown(ctx context.Context, p auth.Principal, data *chatData, es []dto.Entry) {
	if !p.Has(auth.ScopeMessage) {
		return
	}
	var seqs []string
	var nums []int64
	for _, e := range es {
		if e.Kind == dto.EntryMessage && !e.Event && (e.Sender == nil || e.Sender.Name != p.Name) {
			seqs = append(seqs, strconv.FormatInt(e.Seq, 10))
			nums = append(nums, e.Seq)
		}
	}
	if len(nums) == 0 {
		return
	}
	if !assets.BundleBuilt() {
		if _, err := s.chats.svc.Ack(ctx, p, nums); err != nil {
			s.log.Error("acking a rendered chat failed", "err", err)
		}
		return
	}
	data.SeenSrc = "/seen?" + url.Values{"seqs": {strings.Join(seqs, ",")}}.Encode()
}

// pixel is a transparent 1x1 GIF, the answer of the read beacon.
var pixel = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\x00\x00\x00!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

// maxSeen bounds the seqs one beacon acks: a page shows 50 rows.
const maxSeen = 200

// seen is the no-JS read beacon. It writes, so it only answers a request
// from this site's own pages; the session cookie is SameSite=Lax, which a
// cross-site image request does not carry anyway.
func (s *Site) seen(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	SecurityHeaders(w.Header())
	w.Header().Set("Content-Type", "image/gif")
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var seqs []int64
	for _, v := range strings.Split(r.URL.Query().Get("seqs"), ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 && !slices.Contains(seqs, n) {
			seqs = append(seqs, n)
		}
		if len(seqs) == maxSeen {
			break
		}
	}
	if _, err := s.chats.svc.Ack(r.Context(), p, seqs); err != nil {
		s.log.Error("read beacon failed", "err", err)
	}
	_, _ = w.Write(pixel)
}

// chatOptions are the chat picker's choices: every chat the viewer can
// post to, the selected one first.
// chatGroups is the chat picker: the channels of each project, then the
// direct conversations.
func chatGroups(chats []dto.Chat, selected string) []ChatGroup {
	var out []ChatGroup
	at := map[string]int{}
	for _, c := range chats {
		if c.Archived {
			continue
		}
		key, label := "", "Direct"
		if c.Kind == dto.ChatChannel {
			key, label = "p:"+c.Project, c.ProjectTitle
			if label == "" {
				label = c.Project
			}
		}
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			out = append(out, ChatGroup{Label: label})
		}
		out[i].Options = append(out[i].Options, ChatOption{ID: c.ID, Label: ChatName(c), Selected: c.ID == selected})
	}
	return out
}

func (s *Site) dropView(chats []dto.Chat, shareID string, p auth.Principal, back string) QuickDropView {
	v := QuickDropView{Chats: chatGroups(chats, ""), Back: back, MaxFiles: s.chats.svc.Blobs().Config().MaxFiles}
	if sh, ok := s.chats.shares.Get(p.Name, shareID); ok {
		v.ShareID, v.Text = sh.ID, sh.Text
		for _, f := range sh.DTO().Files {
			v.Images = append(v.Images, Image(f))
		}
		if v.Back != "" {
			v.Back = withQuery(back, url.Values{"share": {sh.ID}})
		}
	}
	return v
}

type dropData struct {
	Drop    QuickDropView
	Missing bool // ?share= named a share that is gone
}

func (s *Site) drop(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	chats, err := s.chats.svc.Chats(r.Context(), p, messaging.ChatsQuery{})
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	shareID := r.URL.Query().Get("share")
	data := dropData{Drop: s.dropView(chats, shareID, p, "/drop")}
	data.Missing = shareID != "" && data.Drop.ShareID == ""
	l := s.shellFor(r, p, "Quick drop", "drop")
	l.Back = "/"
	s.Render(w, http.StatusOK, "drop", l, dropTwins, data)
}

// maxShareText bounds the text, title and url fields of a share.
const maxShareText = 8 << 10

// share is the Web Share Target (CONTRACTS 2.4): it stores the files as
// blobs, stages them with the text for an hour, and sends the browser to
// the drop form. The service worker rewrites that redirect to /?share=,
// which opens the quick drop sheet.
func (s *Site) share(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	img := s.chats.svc.Blobs().Config()
	r.Body = http.MaxBytesReader(w, r.Body, int64(img.MaxFiles)*img.MaxFileBytes()+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		s.Fail(w, r, &p, http.StatusBadRequest, "Nothing was shared", "The share did not carry a form cafard can read.")
		return
	}
	var (
		fields = map[string]string{} // title, text, url, joined in that order whatever the form's
		blobs  []dto.Blob
	)
	for {
		part, err := mr.NextPart()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.Fail(w, r, &p, http.StatusBadRequest, "Nothing was shared", "The shared files could not be read; they may be larger than the server accepts.")
				return
			}
			break
		}
		if part.FileName() != "" || part.FormName() == "files[]" || part.FormName() == "files" {
			if len(blobs) >= img.MaxFiles {
				part.Close()
				s.Fail(w, r, &p, http.StatusRequestEntityTooLarge, "Too many files", "Share at most "+strconv.Itoa(img.MaxFiles)+" photos at once.")
				return
			}
			b, err := s.chats.svc.Blobs().Put(r.Context(), part, "")
			part.Close()
			if err != nil {
				var apiErr *dto.APIError
				if errors.As(err, &apiErr) {
					s.Fail(w, r, &p, http.StatusBadRequest, "That file cannot be shared", apiErr.Message)
					return
				}
				s.Oops(w, r, &p, err)
				return
			}
			blobs = append(blobs, b)
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(part, maxShareText))
		part.Close()
		if v := strings.TrimSpace(string(data)); v != "" {
			switch part.FormName() {
			case "title", "text", "url":
				fields[part.FormName()] = v
			}
		}
	}
	for _, b := range blobs {
		if !strings.HasPrefix(b.Mime, "image/") {
			s.Fail(w, r, &p, http.StatusBadRequest, "That file cannot be shared", "Only photos can be shared to cafard.")
			return
		}
	}
	var texts []string
	for _, k := range []string{"title", "text", "url"} {
		if v := fields[k]; v != "" && !slices.Contains(texts, v) {
			texts = append(texts, v)
		}
	}
	sh := s.chats.shares.Put(p.Name, strings.Join(texts, "\n"), blobs)
	http.Redirect(w, r, "/drop?share="+url.QueryEscape(sh.ID), http.StatusSeeOther)
}
