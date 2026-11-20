package pages

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/view"
)

// The data of the chat partials (CONTRACTS.md 6.2) and the builders that
// turn the API's DTOs into them, so a server-rendered row and one the
// bundle builds from the same DTO read alike. Zero values render the
// twins.

// ChatRowView is partials/chat_row.html (tpl-chat-row).
type ChatRowView struct {
	ID, Href, Name, Title string
	Preview, Time, ISO    string
	Unread, Questions     string // "" when zero
	Dots                  []StreamDotView
	Pinned, Muted         bool
	Archived, Active      bool
	Unfollowed            bool
	Items                 string // a gallery's "N images", in the left bar
	Presence              string // a direct conversation's peer: "online" or "offline"
}

// MessageView is partials/message.html (tpl-message).
type MessageView struct {
	Seq                 int64
	ID                  string // "m<seq>", the anchor
	Mine                bool
	Sender, SenderKind  string
	Time, ISO           string
	Segments            []dto.Segment
	Images              []ImageView
	ImageCount          int
	Logs                []LogAttachmentView
	Refs                []GalleryRefView
	ReplyQuote          string
	SeenBy              string
	ChatHref, ChatLabel string // set in lists across chats (the overview)
	ReplyTo             int64
	Text                string // for "copy text"
}

// ImageView is partials/image.html (tpl-image).
type ImageView struct {
	SHA, Href, Src, Alt string
	Width, Height       int
	More                string // "+3" on the last tile of a full grid
	NoPreview           bool   // HEIC: a file tile instead of an image
}

// LogAttachmentView is partials/log_attachment.html (tpl-log-attachment).
type LogAttachmentView struct {
	Streams, Title, Lines, Span, State, Open string
	Preview                                  []LineView
	Snapshot, Expired, OpenEnded             bool
}

// QuestionView is partials/question.html (tpl-question) and the needs you
// card (tpl-needs-you-question).
type QuestionView struct {
	Seq                int64
	ID                 string
	Open               bool
	Sender, SenderKind string
	Time, ISO          string
	Segments           []dto.Segment
	Choices            []ChoiceView
	Answer             string
	// The no-JS answer form.
	Chat, ReplyTo, Back, Next string
	// The chat the question is in, shown in lists across chats.
	ChatHref, ChatLabel string
	InList              bool
}

// ChoiceView is one answer button (tpl-choice).
type ChoiceView struct {
	Label    string
	Disabled bool
}

// EventRowView is partials/event_row.html (tpl-event-row).
type EventRowView struct {
	Seq          int64
	Severity     int
	Kind         string
	Stream, Text string
	Time, ISO    string
	Open         string
}

// AttachmentChipView is partials/attachment_chip.html.
type AttachmentChipView struct {
	Kind, Label string
	Index       int
}

// UploadStatusView is partials/upload_status.html.
type UploadStatusView struct {
	State, Label string
	Progress     int
}

// TimelineItem is one row of a conversation.
type TimelineItem struct {
	Message  *MessageView
	Question *QuestionView
	Event    *EventRowView
}

// MessageTime is the time of a message: hours and minutes today (UTC),
// the date otherwise. src/render/format.ts messageTime matches it.
func MessageTime(t time.Time, now time.Time) string { return dayOrTime(t, now, "15:04") }

// ChatName is how a chat is named in lists and titles: "#dut1", "@codex".
func ChatName(c dto.Chat) string {
	if c.Kind == dto.ChatDirect {
		return "@" + c.Name
	}
	return "#" + c.Name
}

func count(n int) string {
	if n <= 0 {
		return ""
	}
	if n > 99 {
		return "99+"
	}
	return strconv.Itoa(n)
}

// ChatRow is the list row of a chat.
func ChatRow(c dto.Chat, now time.Time, active string) ChatRowView {
	v := ChatRowView{ID: c.ID, Href: view.ChatHref(c.ID), Name: ChatName(c), Title: c.Title,
		Unread: count(c.Unread), Questions: count(c.OpenQuestions),
		Pinned: c.Pinned, Muted: c.Muted, Archived: c.Archived, Active: c.ID == active, Unfollowed: !c.Followed}
	if c.ChannelKind == dto.ChannelGallery {
		v.Href = messaging.GalleryHref(strings.TrimPrefix(c.ID, "chan:"), nil)
	}
	if c.Muted {
		v.Unread = ""
	}
	if c.LastActivity != nil {
		v.Time, v.ISO = MessageTime(c.LastActivity.Time, now), iso(c.LastActivity.Time)
	}
	if c.Preview != nil {
		v.Preview = PreviewText(*c.Preview)
	}
	for _, st := range c.Streams {
		v.Dots = append(v.Dots, StreamDotView{Status: string(st.Status), Title: st.ID + ": " + string(st.Status)})
	}
	return v
}

// PreviewText is a chat row's preview line. render/format.ts
// previewText matches it.
func PreviewText(p dto.ChatPreview) string {
	if p.Sender == "" {
		return view.StripText(p.Text)
	}
	return p.Sender + ": " + view.StripText(p.Text)
}

// SeenText is the receipt line of a message.
func SeenText(by []string) string {
	if len(by) == 0 {
		return ""
	}
	return "seen by " + strings.Join(by, ", ")
}

// AnswerText is the line of an answered question.
func AnswerText(a *dto.Answer) string {
	if a == nil {
		return ""
	}
	if a.Choice == "" {
		return "answered by " + a.By
	}
	return "answered by " + a.By + ": " + a.Choice
}

// maxTiles is how many images a message grid shows before "+N".
const maxTiles = 4

// Images are the image tiles of a message.
func Images(atts []dto.Attachment) []ImageView {
	var imgs []dto.ImageAttachment
	for _, a := range atts {
		if a.Image != nil {
			imgs = append(imgs, *a.Image)
		}
	}
	var out []ImageView
	for i, img := range imgs {
		if i == maxTiles {
			break
		}
		v := Image(img)
		if i == maxTiles-1 && len(imgs) > maxTiles {
			v.More = fmt.Sprintf("+%d", len(imgs)-maxTiles)
		}
		out = append(out, v)
	}
	return out
}

// Image is one image tile.
func Image(img dto.ImageAttachment) ImageView {
	v := ImageView{SHA: img.SHA256, Href: img.URL, Src: img.ThumbURL, Alt: "Photo", Width: img.Width, Height: img.Height}
	if !img.Preview || img.ThumbURL == "" {
		v.NoPreview, v.Src, v.Alt = true, "", "HEIC photo: open to download"
	}
	return v
}

// LogAttachment is the card of a log attachment.
func LogAttachment(a dto.LogAttachment) LogAttachmentView {
	v := LogAttachmentView{Streams: strings.Join(a.Streams, ", "), Title: a.Title, Snapshot: a.Snapshot,
		Expired: a.Expired, OpenEnded: a.ToSeq == nil, Open: LogLink(a)}
	if v.Streams == "" {
		v.Streams = a.Stream + a.Channel
	}
	v.Lines = LinesText(a.Lines, a.ToSeq == nil)
	if a.FirstTS != nil && a.LastTS != nil {
		v.Span = LogTime(a.FirstTS.Time) + " to " + LogTime(a.LastTS.Time)
	}
	switch {
	case a.Expired:
		v.State = "lines expired"
	case a.ToSeq == nil:
		v.State = "open-ended"
	case a.Snapshot:
		v.State = "snapshot"
	}
	for _, e := range a.Preview {
		v.Preview = append(v.Preview, Line(entryOf(e)))
	}
	return v
}

// LinesText is a line count. render/format.ts linesText matches it.
func LinesText(n int, openEnded bool) string {
	s := strconv.Itoa(n) + " lines"
	if n == 1 {
		s = "1 line"
	}
	if openEnded {
		s += " so far"
	}
	return s
}

// LogLink is the no-JS "Open" of a log attachment: its range in /live,
// with its filters.
func LogLink(a dto.LogAttachment) string {
	q := url.Values{}
	q.Set("streams", strings.Join(a.Streams, ","))
	q.Set("from_seq", strconv.FormatInt(a.FromSeq, 10))
	if a.ToSeq != nil {
		q.Set("to_seq", strconv.FormatInt(*a.ToSeq, 10))
	}
	if a.MinSeverity != nil {
		q.Set("sev", strconv.Itoa(*a.MinSeverity))
	}
	if a.Grep != "" {
		q.Set("re", a.Grep)
	}
	return "/live?" + q.Encode()
}

// entryOf rebuilds the store entry of a preview line, for Line.
func entryOf(e dto.Entry) store.Entry {
	return store.Entry{Seq: e.Seq, TS: e.TS.Time, Stream: e.Stream, Kind: store.Kind(e.Kind), Severity: e.Severity, Text: e.Text}
}

// replyQuote is the quote line of a reply: who wrote the message it
// answers, and how it starts.
func replyQuote(e dto.Entry, byseq map[int64]dto.Entry) string {
	if e.ReplyTo == 0 {
		return ""
	}
	if parent, ok := byseq[e.ReplyTo]; ok && parent.Sender != nil {
		return "reply to " + parent.Sender.Name + ": " + view.StripText(firstLine(parent.Text, 80))
	}
	return fmt.Sprintf("reply to message %d", e.ReplyTo)
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i] + " ..."
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// ItemsOptions say how timeline rows are built.
type ItemsOptions struct {
	Viewer string
	Now    time.Time
	// Back and Next are the no-JS answer form's return pages.
	Back, Next string
	// InList shows each row's chat (the overview mixes chats).
	InList bool
}

// Items turns timeline entries into rows.
func Items(es []dto.Entry, o ItemsOptions) []TimelineItem {
	byseq := make(map[int64]dto.Entry, len(es))
	for _, e := range es {
		byseq[e.Seq] = e
	}
	out := make([]TimelineItem, 0, len(es))
	for _, e := range es {
		switch {
		case e.Event || e.Kind != dto.EntryMessage:
			ev := EventRow(e)
			out = append(out, TimelineItem{Event: &ev})
		case e.Question != nil:
			q := Question(e, o)
			out = append(out, TimelineItem{Question: &q})
		default:
			m := Message(e, o, byseq)
			out = append(out, TimelineItem{Message: &m})
		}
	}
	return out
}

func senderOf(e dto.Entry) (name, kind string) {
	if e.Sender == nil {
		return "", ""
	}
	return e.Sender.Name, string(e.Sender.Kind)
}

// Message is the view of a message.
func Message(e dto.Entry, o ItemsOptions, byseq map[int64]dto.Entry) MessageView {
	name, kind := senderOf(e)
	v := MessageView{Seq: e.Seq, ID: "m" + strconv.FormatInt(e.Seq, 10), Mine: name == o.Viewer && name != "",
		Sender: name, SenderKind: kind, Time: MessageTime(e.TS.Time, o.Now), ISO: iso(e.TS.Time),
		Segments: e.Segments, ReplyQuote: replyQuote(e, byseq), SeenBy: SeenText(e.SeenBy), ReplyTo: e.ReplyTo, Text: e.Text}
	if len(v.Segments) == 0 && e.Text != "" {
		v.Segments = []dto.Segment{{T: dto.SegText, V: e.Text}}
	}
	v.Images = Images(e.Attachments)
	v.ImageCount = min(len(v.Images), maxTiles)
	for _, a := range e.Attachments {
		if a.Log != nil {
			v.Logs = append(v.Logs, LogAttachment(*a.Log))
		}
	}
	for _, r := range e.GalleryRefs {
		v.Refs = append(v.Refs, GalleryRef(r))
	}
	if o.InList && e.Chat != "" {
		v.ChatHref, v.ChatLabel = view.ChatHref(e.Chat)+"#"+v.ID, view.ChatLabel(e.Chat)
	}
	return v
}

// GalleryRefView is partials/gallery_ref.html (tpl-gallery-ref).
type GalleryRefView struct {
	State, Href, Title, Meta string
	Section                  bool // a section's strip, not one item
	Thumbs                   []RefThumbView
}

// RefThumbView is one thumbnail of a gallery ref.
type RefThumbView struct {
	Src, Alt, Full string
}

// GalleryRef is the card of a message's gallery ref. What the reader may
// not see says so and nothing else. render/renderers/GalleryRefRenderer.ts
// matches it.
func GalleryRef(r dto.GalleryRef) GalleryRefView {
	switch {
	case r.State == dto.RefUnavailable:
		return GalleryRefView{State: r.State, Title: "gallery item unavailable"}
	case r.State != dto.RefOK:
		return GalleryRefView{State: dto.RefGone, Title: "no longer available"}
	case r.Item != nil:
		return GalleryRefView{State: r.State, Href: r.Href, Title: ItemTitle(*r.Item),
			Meta: "#" + r.Gallery + " / " + SectionLabel(r.Item.Section), Thumbs: refThumbs([]dto.GalleryItem{*r.Item})}
	}
	return GalleryRefView{State: r.State, Href: r.Href, Section: true, Title: SectionLabel(r.Section),
		Meta: ImageCount(r.Count) + " in #" + r.Gallery, Thumbs: refThumbs(r.Items)}
}

func refThumbs(items []dto.GalleryItem) []RefThumbView {
	var out []RefThumbView
	for _, it := range items {
		if it.Image.Preview && it.Image.ThumbURL != "" {
			out = append(out, RefThumbView{Src: it.Image.ThumbURL, Alt: ItemTitle(it), Full: it.Image.URL})
		}
	}
	return out
}

// ItemTitle is an item's title, or "Screenshot <seq>" when it has none.
func ItemTitle(it dto.GalleryItem) string {
	if it.Title != "" {
		return it.Title
	}
	return "Screenshot " + strconv.FormatInt(it.Seq, 10)
}

// ImageCount is "1 image" or "N images".
func ImageCount(n int) string {
	if n == 1 {
		return "1 image"
	}
	return strconv.Itoa(n) + " images"
}

// Question is the view of a question card.
func Question(e dto.Entry, o ItemsOptions) QuestionView {
	name, kind := senderOf(e)
	v := QuestionView{Seq: e.Seq, ID: "m" + strconv.FormatInt(e.Seq, 10), Open: e.Question.Open,
		Sender: name, SenderKind: kind, Time: MessageTime(e.TS.Time, o.Now), ISO: iso(e.TS.Time), Segments: e.Segments,
		Answer: AnswerText(e.Question.Answer), Chat: e.Chat, ReplyTo: strconv.FormatInt(e.Seq, 10),
		Back: o.Back, Next: o.Next, InList: o.InList}
	if len(v.Segments) == 0 && e.Text != "" {
		v.Segments = []dto.Segment{{T: dto.SegText, V: e.Text}}
	}
	for _, c := range e.Question.Choices {
		v.Choices = append(v.Choices, ChoiceView{Label: c, Disabled: !e.Question.Open})
	}
	if o.InList {
		v.ChatHref, v.ChatLabel = view.ChatHref(e.Chat)+"#"+v.ID, "in "+view.ChatLabel(e.Chat)
	}
	return v
}

// EventRow is the view of a line or marker shown between messages.
func EventRow(e dto.Entry) EventRowView {
	return EventRowView{Seq: e.Seq, Severity: e.Severity, Kind: string(e.Kind), Stream: e.Stream,
		Text: view.StripText(e.Text), Time: LogTime(e.TS.Time), ISO: iso(e.TS.Time), Open: EventLink(e.Stream, e.Seq)}
}

// EventLink is where an event row leads without the bundle: its stream
// in /live, up to it. render/format.ts eventLink matches it.
func EventLink(stream string, seq int64) string {
	if store.IsMessageStream(stream) {
		return "" // the chat's own markers have no logs to open
	}
	return "/live?" + url.Values{"streams": {stream}, "to_seq": {strconv.FormatInt(seq, 10)}}.Encode()
}
