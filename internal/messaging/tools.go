package messaging

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/blob"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/textclean"
)

// Tool limits.
const (
	waitDefault        = 60 * time.Second
	waitMax            = 600 * time.Second
	askDefault         = 300 * time.Second
	askMax             = 1800 * time.Second
	getMessageLogLines = 200
	maxImagesPerCall   = 6
)

// entryPage is the structured content of tools that return entries.
type entryPage struct {
	mcpserver.Page
	Entries []dto.Entry `json:"entries"`
}

type sendIn struct {
	Text    string            `json:"text" jsonschema:"the message, at most 32 KiB"`
	To      string            `json:"to,omitempty" jsonschema:"a principal for a direct message, or @humans; none broadcasts in channel"`
	Channel string            `json:"channel,omitempty" jsonschema:"the channel, default general; ignored for a direct message"`
	ReplyTo int64             `json:"reply_to,omitempty" jsonschema:"seq of the message this answers"`
	Images  []dto.ImageUpload `json:"images,omitempty" jsonschema:"at most 6 images: base64 data and mime"`
	Logs    []dto.LogRequest  `json:"logs,omitempty" jsonschema:"at most 5 log slices: {stream or channel, from_seq, to_seq (omit for open-ended), grep, min_severity, title}"`
	Section string            `json:"section,omitempty" jsonschema:"gallery channels only: the batch this screenshot belongs to, e.g. login flow v2 (at most 80 characters)"`
	// GalleryRefs point at gallery images without copying them.
	GalleryRefs []int64 `json:"gallery_refs,omitempty" jsonschema:"chat channels only: seqs of gallery images of the same project to show in the message"`
}

type sendLog struct {
	Index    int      `json:"index"`
	Streams  []string `json:"streams"`
	Lines    int      `json:"lines"`
	Snapshot bool     `json:"snapshot"`
}

type sendOut struct {
	Seq  int64     `json:"seq"`
	Chat string    `json:"chat"`
	Logs []sendLog `json:"logs,omitempty"`
}

type inboxIn struct {
	Channel      string `json:"channel,omitempty" jsonschema:"only this channel"`
	Project      string `json:"project,omitempty" jsonschema:"only the channels of this project"`
	Limit        int    `json:"limit,omitempty" jsonschema:"at most this many, default 20"`
	IncludeAcked bool   `json:"include_acked,omitempty" jsonschema:"also show messages already acked (the latest ones)"`
}

type getMessageIn struct {
	Seq          int64 `json:"seq"`
	Ack          *bool `json:"ack,omitempty" jsonschema:"mark it read, default true"`
	ImagesOffset int   `json:"images_offset,omitempty" jsonschema:"first image to return, for messages with more than 6"`
}

type getMessageOut struct {
	Entry        dto.Entry `json:"entry"`
	ImagesShown  int       `json:"images_shown"`
	ImagesOffset int       `json:"next_images_offset,omitempty"`
	images       []mcp.Content
}

// ExtraContent implements mcpserver.ExtraContent: the images.
func (o getMessageOut) ExtraContent() []mcp.Content { return o.images }

type getLogIn struct {
	Seq      int64 `json:"seq" jsonschema:"the message"`
	Index    int   `json:"index" jsonschema:"the attachment's index in the message, from 0"`
	SinceSeq int64 `json:"since_seq,omitempty" jsonschema:"continue from this seq (the next_seq of the previous call)"`
	Limit    int   `json:"limit,omitempty" jsonschema:"lines, at most 400"`
}

type getLogOut struct {
	mcpserver.Page
	Snapshot bool `json:"snapshot"`
	Expired  bool `json:"expired"`
}

type ackIn struct {
	Seqs []int64 `json:"seqs"`
}

type ackOut struct {
	Acked int `json:"acked"`
}

type waitMessageIn struct {
	From     string `json:"from,omitempty" jsonschema:"only from this principal"`
	Channel  string `json:"channel,omitempty" jsonschema:"only in this channel"`
	ReplyTo  int64  `json:"reply_to,omitempty" jsonschema:"only replies to this message (e.g. an ask_human question)"`
	TimeoutS int    `json:"timeout_s,omitempty" jsonschema:"seconds, default 60, at most 600"`
	Ack      *bool  `json:"ack,omitempty" jsonschema:"mark the message read, default true"`
}

type waitMessageOut struct {
	TimedOut bool       `json:"timed_out"`
	Entry    *dto.Entry `json:"entry,omitempty"`
}

type askIn struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices,omitempty" jsonschema:"buttons the human can press, e.g. [\"done\", \"can't\"]"`
	Channel  string   `json:"channel,omitempty" jsonschema:"default general"`
	TimeoutS *int     `json:"timeout_s,omitempty" jsonschema:"seconds to wait for the answer, default 300, at most 1800; 0 returns at once"`
}

type askAnswer struct {
	By     string `json:"by"`
	Choice string `json:"choice,omitempty"`
	Text   string `json:"text"`
	Seq    int64  `json:"seq"`
}

type askOut struct {
	Seq      int64      `json:"seq"`
	TimedOut bool       `json:"timed_out"`
	Answer   *askAnswer `json:"answer,omitempty"`
}

type listPrincipalsIn struct {
	Kind string `json:"kind,omitempty" jsonschema:"agent, human or device"`
}

type listPrincipalsOut struct {
	Principals []dto.Principal `json:"principals"`
}

type listChannelsIn struct {
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Project         string `json:"project,omitempty" jsonschema:"only the channels of this project"`
}

type listChannelsOut struct {
	Channels []dto.Chat `json:"channels"`
}

type createChannelIn struct {
	Name    string          `json:"name" jsonschema:"lowercase slug, e.g. run-42, or project/run-42"`
	Project string          `json:"project,omitempty" jsonschema:"the project, needed when you may make channels in several"`
	Title   string          `json:"title,omitempty"`
	Topic   string          `json:"topic,omitempty"`
	Streams []string        `json:"streams,omitempty" jsonschema:"stream ids or globs of the channel's project to bind, e.g. serial:dut1, syslog:dut1*"`
	Surface dto.Surface     `json:"surface,omitempty" jsonschema:"none, markers (default) or errors: which entries of bound streams the chat shows"`
	Kind    dto.ChannelKind `json:"kind,omitempty" jsonschema:"chat (default) or gallery: a gallery holds screenshots, one image and a title per message"`
	Private bool            `json:"private,omitempty" jsonschema:"a private channel in your own space (~<you>/name), read by you and members only; no project, no streams"`
	Members []string        `json:"members,omitempty" jsonschema:"with private: principals to add, each sharing a project with you"`
}

type channelOut struct {
	Channel dto.Chat `json:"channel"`
}

type updateChannelIn struct {
	Name          string          `json:"name"`
	Rename        string          `json:"rename,omitempty"`
	Title         *string         `json:"title,omitempty"`
	Topic         *string         `json:"topic,omitempty"`
	AddStreams    []string        `json:"add_streams,omitempty"`
	RemoveStreams []string        `json:"remove_streams,omitempty"`
	Surface       dto.Surface     `json:"surface,omitempty"`
	Archived      *bool           `json:"archived,omitempty"`
	Kind          dto.ChannelKind `json:"kind,omitempty" jsonschema:"chat or gallery"`
}

type nameIn struct {
	Name string `json:"name"`
}

type deleteOut struct {
	Deleted string `json:"deleted"`
}

type followIn struct {
	Channel string `json:"channel"`
}

type followOut struct {
	Channel  string `json:"channel"`
	Followed bool   `json:"followed"`
}

type timelineIn struct {
	Channel  string `json:"channel"`
	SinceSeq *int64 `json:"since_seq,omitempty" jsonschema:"continue from here; default the latest rows"`
	Limit    int    `json:"limit,omitempty" jsonschema:"rows, default 50, at most 400"`
}

type claimIn struct {
	Resource string `json:"resource" jsonschema:"a stream id, e.g. serial:dut1"`
	TTLS     int    `json:"ttl_s,omitempty" jsonschema:"seconds, default 900, at most 86400; claim again to renew"`
	Note     string `json:"note,omitempty" jsonschema:"what you are doing, shown to others"`
}

type claimDTO struct {
	Resource  string   `json:"resource"`
	Principal string   `json:"principal"`
	Note      string   `json:"note,omitempty"`
	ExpiresAt dto.Time `json:"expires_at"`
}

type claimOut struct {
	Claim   claimDTO `json:"claim"`
	Renewed bool     `json:"renewed"`
}

type releaseIn struct {
	Resource string `json:"resource"`
}

type releaseOut struct {
	Released string `json:"released"`
}

type listClaimsIn struct {
	Channel string `json:"channel,omitempty" jsonschema:"only claims on the streams bound to this channel"`
}

type listClaimsOut struct {
	Claims []claimDTO `json:"claims"`
}

func toClaimDTO(c ClaimRow) claimDTO {
	return claimDTO{Resource: c.Resource, Principal: c.Principal, Note: c.Note, ExpiresAt: dto.TimeOf(c.ExpiresAt)}
}

// RegisterTools adds the message, channel and claim tools, and the
// resources.
func (s *Service) RegisterTools(m *mcpserver.Server) {
	r, w := auth.ScopeRead, auth.ScopeMessage
	mcpserver.AddTool(m, r, mcp.Tool{Name: "list_projects", Description: "The projects you have a grant in: your scopes there, their channels, their galleries (where screenshots go) with their sections, and their sinks. Name a channel project/name when several have it."}, s.toolListProjects)
	mcpserver.AddTool(m, r, mcp.Tool{Name: "list_principals", Description: "Who can be messaged (those you share a project with): name, kind (agent, human, device), last seen."}, s.toolListPrincipals)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "send", Description: "Send a message: to a principal (direct), to @humans in a channel, or to a whole channel (default general). Attach images and log slices. In a gallery channel a message is a screenshot: an image, a short title as text, and a section for a batch. In a chat channel, gallery_refs (or [[project/gallery#seq]] in the text) show gallery images without copying them. Do not send acknowledgements or thanks."}, s.toolSend)
	mcpserver.AddTool(m, r, mcp.Tool{Name: "inbox", Description: "Your unread messages: direct ones, those to @humans if you are human, and broadcasts in channels you follow. Check it at the start of a task and whenever you wait."}, s.toolInbox)
	mcpserver.AddTool(m, r, mcp.Tool{Name: "get_message", Description: "One message in full: text, its images, and the first lines of its log attachments. Marks it read unless ack=false."}, s.toolGetMessage)
	mcpserver.AddTool(m, r, mcp.Tool{Name: "get_log_attachment", Description: "The lines of a message's log attachment, page by page; an open-ended one keeps delivering new lines."}, s.toolGetLog)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "ack", Description: "Mark messages read (for you only)."}, s.toolAck)
	mcpserver.AddTool(m, r, mcp.Tool{Name: "wait_message", Description: "Wait for the next message for you (or a reply to reply_to), or timed_out."}, s.toolWaitMessage)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "ask_human", Description: "Ask the humans something, with optional choice buttons, and wait for the first answer. Use it when you need a physical action."}, s.toolAskHuman)
	mcpserver.AddTool(m, r, mcp.Tool{Name: "list_channels", Description: "The channels: kind (chat or gallery, with a gallery's sections), topic, bound streams and their status, followed or not, unread count, last activity."}, s.toolListChannels)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "create_channel", Description: "Create a channel, optionally bound to streams (e.g. run-42 bound to serial:dut1), or a gallery for screenshots (kind=gallery); private=true makes it in your own space for you and the members you name. You follow it."}, s.toolCreateChannel)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "update_channel", Description: "Change a channel: title, topic, bindings, surface; rename and archive are for its creator or an admin. Binding changes leave a marker."}, s.toolUpdateChannel)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "delete_channel", Description: "Delete a channel and its messages (creator or admin, runtime channels only)."}, s.toolDeleteChannel)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "follow", Description: "Follow a channel: its broadcasts reach your inbox."}, s.toolFollow)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "unfollow", Description: "Stop following a channel."}, s.toolUnfollow)
	mcpserver.AddTool(m, r, mcp.Tool{Name: "channel_timeline", Description: "A channel as its chat shows it: messages, and markers or errors of its bound streams."}, s.toolTimeline)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "claim", Description: "Claim a stream (a device) before driving it; claim again to renew. Refused while someone else holds it."}, s.toolClaim)
	mcpserver.AddTool(m, w, mcp.Tool{Name: "release", Description: "Release your claim on a stream."}, s.toolRelease)
	mcpserver.AddTool(m, r, mcp.Tool{Name: "list_claims", Description: "Who holds which stream, and until when."}, s.toolListClaims)
	s.registerResources(m)
}

func (s *Service) toolListPrincipals(ctx context.Context, c *mcpserver.Call, in listPrincipalsIn) (string, listPrincipalsOut, error) {
	rows, err := s.tokens.Principals(ctx, in.Kind)
	if err == nil {
		rows, err = s.projects.Reachable(ctx, c.Principal, rows)
	}
	if err != nil {
		return "", listPrincipalsOut{}, err
	}
	out := listPrincipalsOut{Principals: []dto.Principal{}}
	var b strings.Builder
	for _, r := range rows {
		p := dto.Principal{Name: r.Name, Kind: dto.PrincipalKind(r.Kind)}
		last := "never"
		if r.LastSeen != nil {
			p.LastSeen = dto.TimePtr(*r.LastSeen)
			last = r.LastSeen.UTC().Format("2006-01-02 15:04Z")
		}
		out.Principals = append(out.Principals, p)
		fmt.Fprintf(&b, "%s (%s), last seen %s\n", r.Name, r.Kind, last)
	}
	if len(rows) == 0 {
		return "No principals.", out, nil
	}
	return b.String(), out, nil
}

func (s *Service) toolSend(ctx context.Context, c *mcpserver.Call, in sendIn) (string, sendOut, error) {
	e, err := s.SendMessage(ctx, c.Principal, SendInput{SendRequest: dto.SendRequest{
		Text: in.Text, To: in.To, Channel: in.Channel, ReplyTo: in.ReplyTo, Images: in.Images, Logs: in.Logs,
		Section: in.Section, GalleryRefs: in.GalleryRefs}})
	if err != nil {
		return "", sendOut{}, err
	}
	out := sendOut{Seq: e.Seq, Chat: ChatID(c.Principal.Name, e.Stream)}
	var b strings.Builder
	fmt.Fprintf(&b, "Sent as seq %d in %s.", e.Seq, out.Chat)
	li := 0
	for _, a := range e.Attachments {
		if a.Type != dto.AttachmentLog {
			continue
		}
		out.Logs = append(out.Logs, sendLog{Index: li, Streams: a.Streams, Lines: a.Lines, Snapshot: a.SnapshotSHA256 != ""})
		fmt.Fprintf(&b, " Log attachment %d: %d lines from %s.", li+1, a.Lines, strings.Join(a.Streams, ", "))
		li++
	}
	return b.String(), out, nil
}

// summary is one inbox or timeline line about a message.
func (s *Service) summary(ctx context.Context, viewer auth.Principal, e store.Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s %s in %s", e.Seq, e.TS.UTC().Format("2006-01-02 15:04:05Z"), e.Sender, ChatID(viewer.Name, e.Stream))
	if e.Recipient != "" && !strings.HasPrefix(e.Stream, "dm:") {
		fmt.Fprintf(&b, " to %s", e.Recipient)
	}
	if e.ReplyTo != 0 {
		fmt.Fprintf(&b, " (reply to %d)", e.ReplyTo)
	}
	b.WriteString(": ")
	b.WriteString(previewText(textclean.StripANSI(e.Text), 200))
	imgs, logs := 0, 0
	for _, a := range e.Attachments {
		if a.Type == dto.AttachmentImage {
			imgs++
		} else {
			logs++
		}
	}
	if imgs > 0 {
		fmt.Fprintf(&b, " [%d image(s)]", imgs)
	}
	if logs > 0 {
		fmt.Fprintf(&b, " [%d log attachment(s)]", logs)
	}
	m := readMeta(e)
	if len(m.Refs) > 0 {
		b.WriteString(" " + GalleryImagesLabel(len(m.Refs)))
	}
	if m.Section != "" {
		fmt.Fprintf(&b, " [section %q]", m.Section)
	}
	if m.Choices != nil {
		if a, _ := s.answer(ctx, e.Seq); a == nil {
			fmt.Fprintf(&b, " [open question")
		} else {
			fmt.Fprintf(&b, " [answered question")
		}
		if len(*m.Choices) > 0 {
			fmt.Fprintf(&b, ": %s", strings.Join(*m.Choices, " | "))
		}
		b.WriteString("]")
	}
	return b.String()
}

func (s *Service) toolInbox(ctx context.Context, c *mcpserver.Call, in inboxIn) (string, entryPage, error) {
	list, more, err := s.Inbox(ctx, c.Principal, InboxQuery{Channel: in.Channel, Project: in.Project, Limit: in.Limit, IncludeAcked: in.IncludeAcked})
	if err != nil {
		return "", entryPage{}, err
	}
	out := entryPage{Entries: []dto.Entry{}, Page: mcpserver.Page{HasMore: more, Shown: len(list)}}
	var b strings.Builder
	for _, e := range list {
		d, err := s.EntryDTO(ctx, c.Principal, e)
		if err != nil {
			return "", entryPage{}, err
		}
		out.Entries = append(out.Entries, d)
		b.WriteString(s.summary(ctx, c.Principal, e))
		b.WriteByte('\n')
		out.NextSeq = e.Seq + 1
	}
	if len(list) == 0 {
		return "Your inbox is empty.", out, nil
	}
	if more {
		b.WriteString("[more messages: ack these, or read them with get_message, then call inbox again]\n")
	} else if !in.IncludeAcked {
		b.WriteString("Read one with get_message(seq), which marks it read.\n")
	}
	return b.String(), out, nil
}

func (s *Service) toolGetMessage(ctx context.Context, c *mcpserver.Call, in getMessageIn) (string, getMessageOut, error) {
	e, err := s.Message(ctx, c.Principal, in.Seq)
	if err != nil {
		return "", getMessageOut{}, err
	}
	d, err := s.EntryDTO(ctx, c.Principal, e)
	if err != nil {
		return "", getMessageOut{}, err
	}
	out := getMessageOut{Entry: d}
	var b strings.Builder
	kind := s.kindOf(ctx, e.Sender)
	fmt.Fprintf(&b, "Message %d from %s (%s) in %s at %s", e.Seq, e.Sender, kind, ChatID(c.Principal.Name, e.Stream), e.TS.UTC().Format(time.RFC3339))
	if e.Recipient != "" && !strings.HasPrefix(e.Stream, "dm:") {
		fmt.Fprintf(&b, ", to %s", e.Recipient)
	}
	if e.ReplyTo != 0 {
		fmt.Fprintf(&b, ", reply to %d", e.ReplyTo)
	}
	b.WriteString(":\n")
	b.WriteString(e.Text)
	b.WriteString("\n")
	if d.Question != nil {
		if d.Question.Answer != nil {
			fmt.Fprintf(&b, "Question answered by %s (seq %d)", d.Question.Answer.By, d.Question.Answer.Seq)
			if d.Question.Answer.Choice != "" {
				fmt.Fprintf(&b, ": %s", d.Question.Answer.Choice)
			}
			b.WriteString(".\n")
		} else {
			fmt.Fprintf(&b, "Open question; choices: %s.\n", strings.Join(d.Question.Choices, " | "))
		}
	}

	var images []store.Attachment
	for _, a := range e.Attachments {
		if a.Type == dto.AttachmentImage {
			images = append(images, a)
		}
	}
	offset := max(0, in.ImagesOffset)
	for i := offset; i < len(images) && out.ImagesShown < maxImagesPerCall; i++ {
		a := images[i]
		if a.Mime == blob.MimeHEIC {
			fmt.Fprintf(&b, "Image %d of %d: HEIC, %dx%d. It has no preview and cannot be shown; ask the sender for a JPEG.\n", i+1, len(images), a.Width, a.Height)
			continue
		}
		data, row, err := s.blobs.ReadAll(ctx, a.SHA256, blob.VariantAgent)
		if err != nil {
			fmt.Fprintf(&b, "Image %d of %d: no longer available.\n", i+1, len(images))
			continue
		}
		out.images = append(out.images, &mcp.ImageContent{Data: data, MIMEType: row.Mime})
		out.ImagesShown++
		fmt.Fprintf(&b, "Image %d of %d: %s %dx%d, attached below at %dx%d.\n", i+1, len(images), a.Mime, a.Width, a.Height, row.Width, row.Height)
		if out.ImagesShown == maxImagesPerCall && i+1 < len(images) {
			out.ImagesOffset = i + 1
			fmt.Fprintf(&b, "More images: call get_message(seq=%d, images_offset=%d).\n", e.Seq, i+1)
		}
	}

	s.describeRefs(ctx, &b, &out, d.GalleryRefs)

	li := -1
	for idx, a := range e.Attachments {
		if a.Type != dto.AttachmentLog {
			continue
		}
		li++
		fmt.Fprintf(&b, "\nLog attachment %d (index %d): %s\n", li+1, idx, describeRange(a))
		if !s.CanReadAttachment(ctx, c.Principal, a) {
			b.WriteString("You cannot read every stream it covers, so its lines are not shown; ask the admin for a grant.\n")
			continue
		}
		page, err := s.attachmentPage(ctx, a, a.FromSeq, getMessageLogLines)
		if err != nil {
			return "", getMessageOut{}, err
		}
		if len(page.Entries) == 0 {
			if exp, _ := s.expired(ctx, a); exp {
				b.WriteString("Retention has removed these lines; no snapshot was kept.\n")
			} else {
				b.WriteString("No lines yet.\n")
			}
		} else {
			f := textclean.Format(page.Entries, textclean.AgentDefaults, time.UTC)
			b.WriteString(f.Text)
			if f.HasMore {
				page.NextSeq, page.HasMore = f.NextSeq, true
			}
		}
		if page.HasMore || a.ToSeq == nil {
			fmt.Fprintf(&b, "Continue with get_log_attachment(seq=%d, index=%d, since_seq=%d)", e.Seq, idx, page.NextSeq)
			if !page.Snapshot {
				fmt.Fprintf(&b, ", or read(%s)", readArgs(a, page.NextSeq))
			}
			b.WriteString(".\n")
		}
	}
	if len(d.SeenBy) > 0 {
		fmt.Fprintf(&b, "\nSeen by: %s.\n", strings.Join(d.SeenBy, ", "))
	}
	if in.Ack == nil || *in.Ack {
		if _, err := s.Ack(ctx, c.Principal, []int64{e.Seq}); err != nil {
			return "", getMessageOut{}, err
		}
	}
	return b.String(), out, nil
}

// readArgs are the read tool arguments that continue an attachment.
func readArgs(a store.Attachment, since int64) string {
	parts := []string{fmt.Sprintf("streams=[%s]", quoteList(a.Streams)), fmt.Sprintf("since_seq=%d", since)}
	if a.ToSeq != nil {
		parts = append(parts, fmt.Sprintf("until_seq=%d", *a.ToSeq))
	}
	if a.Grep != "" {
		parts = append(parts, fmt.Sprintf("grep=%q", a.Grep))
	}
	if a.MinSeverity != nil {
		parts = append(parts, fmt.Sprintf("min_severity=%d", *a.MinSeverity))
	}
	return strings.Join(parts, ", ")
}

func quoteList(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

func (s *Service) toolGetLog(ctx context.Context, c *mcpserver.Call, in getLogIn) (string, getLogOut, error) {
	limit := in.Limit
	if limit <= 0 || limit > textclean.AgentDefaults.MaxLines {
		limit = textclean.AgentDefaults.MaxLines
	}
	page, a, err := s.AttachmentLines(ctx, c.Principal, in.Seq, in.Index, in.SinceSeq, limit)
	if err != nil {
		return "", getLogOut{}, err
	}
	out := getLogOut{Page: mcpserver.Page{NextSeq: page.NextSeq, HasMore: page.HasMore, Streams: a.Streams}, Snapshot: page.Snapshot, Expired: page.Expired}
	if page.Expired {
		return "Retention has removed these lines and no snapshot was kept.", out, nil
	}
	if len(page.Entries) == 0 {
		if a.ToSeq == nil {
			return fmt.Sprintf("No new lines yet. Call again with since_seq=%d, or wait_for on %s.", page.NextSeq, strings.Join(a.Streams, ", ")), out, nil
		}
		return "No more lines in this attachment.", out, nil
	}
	f := textclean.Format(page.Entries, textclean.AgentDefaults, time.UTC)
	out.Shown = f.Shown
	if f.HasMore {
		out.NextSeq, out.HasMore = f.NextSeq, true
	}
	text := f.Text
	if out.HasMore && !f.HasMore {
		text += fmt.Sprintf("[more available: continue with since_seq=%d]\n", out.NextSeq)
	} else if !out.HasMore && a.ToSeq == nil {
		text += fmt.Sprintf("[open-ended: call again with since_seq=%d for new lines]\n", out.NextSeq)
	}
	return text, out, nil
}

func (s *Service) toolAck(ctx context.Context, c *mcpserver.Call, in ackIn) (string, ackOut, error) {
	n, err := s.Ack(ctx, c.Principal, in.Seqs)
	if err != nil {
		return "", ackOut{}, err
	}
	return fmt.Sprintf("%d message(s) marked read.", n), ackOut{Acked: n}, nil
}

func clampWait(seconds int, def, max time.Duration) time.Duration {
	if seconds <= 0 {
		return def
	}
	return min(time.Duration(seconds)*time.Second, max)
}

func (s *Service) toolWaitMessage(ctx context.Context, c *mcpserver.Call, in waitMessageIn) (string, waitMessageOut, error) {
	timeout := clampWait(in.TimeoutS, waitDefault, waitMax)
	e, ok, err := s.Wait(ctx, c.Principal, WaitQuery{From: in.From, Channel: in.Channel, ReplyTo: in.ReplyTo, Timeout: timeout,
		Progress: func(d time.Duration) { c.Progress(ctx, d.Seconds(), "waiting for a message") }})
	if err != nil {
		return "", waitMessageOut{}, err
	}
	if !ok {
		return fmt.Sprintf("Timed out after %s: no message arrived.", timeout), waitMessageOut{TimedOut: true}, nil
	}
	d, err := s.EntryDTO(ctx, c.Principal, e)
	if err != nil {
		return "", waitMessageOut{}, err
	}
	if in.Ack == nil || *in.Ack {
		if _, err := s.Ack(ctx, c.Principal, []int64{e.Seq}); err != nil {
			return "", waitMessageOut{}, err
		}
	}
	text := s.summary(ctx, c.Principal, e) + "\n" + e.Text + "\n"
	if len(e.Attachments) > 0 {
		text += fmt.Sprintf("It has attachments: get_message(seq=%d) shows them.\n", e.Seq)
	}
	return text, waitMessageOut{Entry: &d}, nil
}

func (s *Service) toolAskHuman(ctx context.Context, c *mcpserver.Call, in askIn) (string, askOut, error) {
	timeout := askDefault
	if in.TimeoutS != nil {
		if *in.TimeoutS <= 0 {
			timeout = 0
		} else {
			timeout = min(time.Duration(*in.TimeoutS)*time.Second, askMax)
		}
	}
	res, err := s.AskHuman(ctx, c.Principal, in.Question, in.Choices, in.Channel, timeout,
		func(d time.Duration) { c.Progress(ctx, d.Seconds(), "waiting for a human to answer") })
	if err != nil {
		return "", askOut{}, err
	}
	out := askOut{Seq: res.Question.Seq}
	where := ChatID(c.Principal.Name, res.Question.Stream)
	if timeout == 0 {
		return fmt.Sprintf("Asked as seq %d in %s. Collect the answer with wait_message(reply_to=%d).", out.Seq, where, out.Seq), out, nil
	}
	if res.Reply == nil {
		out.TimedOut = true
		return fmt.Sprintf("No answer within %s. The question (seq %d in %s) stays open; collect the answer later with wait_message(reply_to=%d) or inbox.",
			timeout, out.Seq, where, out.Seq), out, nil
	}
	out.Answer = &askAnswer{By: res.Reply.Sender, Choice: res.Choice, Text: res.Reply.Text, Seq: res.Reply.Seq}
	if _, err := s.Ack(ctx, c.Principal, []int64{res.Reply.Seq}); err != nil {
		return "", askOut{}, err
	}
	text := fmt.Sprintf("%s answered (seq %d)", res.Reply.Sender, res.Reply.Seq)
	if res.Choice != "" {
		text += fmt.Sprintf(" with %q", res.Choice)
	}
	if res.Reply.Text != "" && res.Reply.Text != res.Choice {
		text += ": " + res.Reply.Text
	}
	return text + ".", out, nil
}

func (s *Service) toolListChannels(ctx context.Context, c *mcpserver.Call, in listChannelsIn) (string, listChannelsOut, error) {
	chs, err := s.Channels(ctx, c.Principal, in.IncludeArchived)
	if err != nil {
		return "", listChannelsOut{}, err
	}
	out := listChannelsOut{Channels: []dto.Chat{}}
	var b strings.Builder
	for _, ch := range chs {
		if in.Project != "" && ch.Project() != in.Project {
			continue
		}
		chat, err := s.ChannelChat(ctx, c.Principal, ch)
		if err != nil {
			return "", listChannelsOut{}, err
		}
		if ch.Gallery() {
			if chat.Sections, err = s.GallerySections(ctx, ch); err != nil {
				return "", listChannelsOut{}, err
			}
		}
		out.Channels = append(out.Channels, chat)
		b.WriteString(channelLine(chat))
		b.WriteByte('\n')
	}
	if len(out.Channels) == 0 {
		return "You can read no channel; list_projects shows your grants.", out, nil
	}
	return b.String(), out, nil
}

func channelLine(c dto.Chat) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%s", strings.TrimPrefix(c.ID, "chan:"))
	if c.Title != "" {
		fmt.Fprintf(&b, " %q", c.Title)
	}
	if c.ChannelKind == dto.ChannelGallery {
		b.WriteString(", a gallery (post screenshots here)")
		if len(c.Sections) > 0 {
			parts := make([]string, len(c.Sections))
			for i, sec := range c.Sections {
				name := sec.Name
				if name == "" {
					name = "(unsorted)"
				}
				parts[i] = fmt.Sprintf("%s: %d", name, sec.Items)
			}
			fmt.Fprintf(&b, "; sections, newest first: %s", strings.Join(parts, ", "))
		}
	}
	if c.Topic != "" {
		fmt.Fprintf(&b, ", topic: %s", c.Topic)
	}
	if len(c.Patterns) > 0 {
		var st []string
		for _, s := range c.Streams {
			st = append(st, s.ID+" ("+string(s.Status)+")")
		}
		fmt.Fprintf(&b, "; bound to %s", strings.Join(c.Patterns, ", "))
		if len(st) > 0 {
			fmt.Fprintf(&b, " = %s", strings.Join(st, ", "))
		} else {
			b.WriteString(" (no stream matches yet)")
		}
		fmt.Fprintf(&b, "; surface %s", c.Surface)
	}
	if c.Followed {
		b.WriteString("; followed")
	} else {
		b.WriteString("; not followed")
	}
	fmt.Fprintf(&b, "; %d unread", c.Unread)
	if c.OpenQuestions > 0 {
		fmt.Fprintf(&b, ", %d open question(s)", c.OpenQuestions)
	}
	if c.LastActivity != nil {
		fmt.Fprintf(&b, "; last activity %s", c.LastActivity.UTC().Format("2006-01-02 15:04Z"))
	}
	if c.Archived {
		b.WriteString("; archived")
	}
	if c.Source == dto.SourceConfig {
		b.WriteString("; from the config")
	}
	return b.String()
}

func (s *Service) chatOut(ctx context.Context, p auth.Principal, ch Channel, verb string) (string, channelOut, error) {
	chat, err := s.ChannelChat(ctx, p, ch)
	if err != nil {
		return "", channelOut{}, err
	}
	return verb + " " + channelLine(chat), channelOut{Channel: chat}, nil
}

func (s *Service) toolCreateChannel(ctx context.Context, c *mcpserver.Call, in createChannelIn) (string, channelOut, error) {
	ch, err := s.CreateChannel(ctx, c.Principal, dto.ChannelCreate{Name: in.Name, Project: in.Project, Title: in.Title, Topic: in.Topic, Patterns: in.Streams, Surface: in.Surface, Kind: in.Kind,
		Private: in.Private, Members: in.Members})
	if err != nil {
		return "", channelOut{}, err
	}
	return s.chatOut(ctx, c.Principal, ch, "Created")
}

func (s *Service) toolUpdateChannel(ctx context.Context, c *mcpserver.Call, in updateChannelIn) (string, channelOut, error) {
	ch, err := s.UpdateChannel(ctx, c.Principal, in.Name, dto.ChannelUpdate{Rename: in.Rename, Title: in.Title, Topic: in.Topic,
		AddPatterns: in.AddStreams, RemovePatterns: in.RemoveStreams, Surface: in.Surface, Archived: in.Archived, Kind: in.Kind})
	if err != nil {
		return "", channelOut{}, err
	}
	return s.chatOut(ctx, c.Principal, ch, "Updated")
}

func (s *Service) toolDeleteChannel(ctx context.Context, c *mcpserver.Call, in nameIn) (string, deleteOut, error) {
	ch, err := s.Channel(ctx, c.Principal, in.Name)
	if err != nil {
		return "", deleteOut{}, err
	}
	if err := s.DeleteChannel(ctx, c.Principal, ch.Name); err != nil {
		return "", deleteOut{}, err
	}
	return fmt.Sprintf("Deleted #%s and its messages.", ch.Name), deleteOut{Deleted: ch.Name}, nil
}

func (s *Service) follow(ctx context.Context, c *mcpserver.Call, channel string, on bool) (string, followOut, error) {
	ch, err := s.Channel(ctx, c.Principal, channel)
	if err != nil {
		return "", followOut{}, err
	}
	st, err := s.SetFollow(ctx, c.Principal, ch.Name, on)
	if err != nil {
		return "", followOut{}, err
	}
	name := ch.Name
	if st.Followed {
		return fmt.Sprintf("You follow #%s: its broadcasts reach your inbox.", name), followOut{Channel: name, Followed: true}, nil
	}
	return fmt.Sprintf("You no longer follow #%s; direct messages and @humans still reach you.", name), followOut{Channel: name}, nil
}

func (s *Service) toolFollow(ctx context.Context, c *mcpserver.Call, in followIn) (string, followOut, error) {
	return s.follow(ctx, c, in.Channel, true)
}

func (s *Service) toolUnfollow(ctx context.Context, c *mcpserver.Call, in followIn) (string, followOut, error) {
	return s.follow(ctx, c, in.Channel, false)
}

func (s *Service) toolTimeline(ctx context.Context, c *mcpserver.Call, in timelineIn) (string, entryPage, error) {
	ch, err := s.Channel(ctx, c.Principal, in.Channel)
	if err != nil {
		return "", entryPage{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, textclean.AgentDefaults.MaxLines)
	q := TimelineQuery{Limit: limit}
	if in.SinceSeq != nil {
		q.SinceSeq = *in.SinceSeq
	} else {
		q.BeforeSeq = 1 << 62
	}
	page, err := s.Timeline(ctx, c.Principal, ch.Stream(), q)
	if err != nil {
		return "", entryPage{}, err
	}
	if in.SinceSeq == nil {
		last, err := s.st.LastSeq(ctx)
		if err != nil {
			return "", entryPage{}, err
		}
		page.NextSeq, page.HasMore = last+1, false
	}
	dp, err := s.TimelineDTO(ctx, c.Principal, page)
	if err != nil {
		return "", entryPage{}, err
	}
	entries := page.entries()
	out := entryPage{Entries: dp.Items, Page: mcpserver.Page{NextSeq: page.NextSeq, HasMore: page.HasMore, Shown: len(entries)}}
	if out.Entries == nil {
		out.Entries = []dto.Entry{}
	}
	if len(entries) == 0 {
		return fmt.Sprintf("#%s has nothing to show yet. Continue with since_seq=%d.", ch.Name, out.NextSeq), out, nil
	}
	f := textclean.Format(entries, textclean.AgentDefaults, time.UTC)
	out.Shown = f.Shown
	text := f.Text
	if f.HasMore {
		out.NextSeq, out.HasMore = f.NextSeq, true
	} else if page.HasMore {
		text += fmt.Sprintf("[more available: continue with since_seq=%d]\n", out.NextSeq)
	}
	return text, out, nil
}

func (s *Service) toolClaim(ctx context.Context, c *mcpserver.Call, in claimIn) (string, claimOut, error) {
	cl, renewed, err := s.Claim(ctx, c.Principal, in.Resource, time.Duration(in.TTLS)*time.Second, in.Note)
	if err != nil {
		return "", claimOut{}, err
	}
	verb := "Claimed"
	if renewed {
		verb = "Renewed your claim on"
	}
	return fmt.Sprintf("%s %s until %s. Release it when done.", verb, cl.Resource, cl.ExpiresAt.Format("15:04:05 UTC")),
		claimOut{Claim: toClaimDTO(cl), Renewed: renewed}, nil
}

func (s *Service) toolRelease(ctx context.Context, c *mcpserver.Call, in releaseIn) (string, releaseOut, error) {
	if err := s.Release(ctx, c.Principal, in.Resource); err != nil {
		return "", releaseOut{}, err
	}
	return fmt.Sprintf("Released %s.", in.Resource), releaseOut{Released: in.Resource}, nil
}

func (s *Service) toolListClaims(ctx context.Context, c *mcpserver.Call, in listClaimsIn) (string, listClaimsOut, error) {
	var patterns []string
	if in.Channel != "" {
		var err error
		if _, patterns, err = s.ResolveChannel(ctx, c.Principal, in.Channel); err != nil {
			return "", listClaimsOut{}, err
		}
	}
	list, err := s.listClaims(ctx, patterns)
	if err != nil {
		return "", listClaimsOut{}, err
	}
	a := s.access(ctx, c.Principal)
	list = slices.DeleteFunc(list, func(cl ClaimRow) bool { return !a.Can(cl.Resource, auth.ScopeRead) })
	out := listClaimsOut{Claims: []claimDTO{}}
	var b strings.Builder
	for _, cl := range list {
		out.Claims = append(out.Claims, toClaimDTO(cl))
		fmt.Fprintf(&b, "%s claimed by %s until %s%s\n", cl.Resource, cl.Principal, cl.ExpiresAt.Format("15:04:05 UTC"), notePart(cl.Note))
	}
	if len(list) == 0 {
		return "No stream is claimed.", out, nil
	}
	return b.String(), out, nil
}

// describeRefs adds a message's gallery refs to get_message: each item
// with its image while the call has room, each section with its newest
// items. What the caller may not see is only said to be unavailable.
func (s *Service) describeRefs(ctx context.Context, b *strings.Builder, out *getMessageOut, refs []dto.GalleryRef) {
	show := func(it dto.GalleryItem) string {
		if out.ImagesShown >= maxImagesPerCall || !it.Image.Preview {
			return "not attached"
		}
		data, row, err := s.blobs.ReadAll(ctx, it.Image.SHA256, blob.VariantAgent)
		if err != nil {
			return "no longer available"
		}
		out.images = append(out.images, &mcp.ImageContent{Data: data, MIMEType: row.Mime})
		out.ImagesShown++
		return fmt.Sprintf("attached below at %dx%d", row.Width, row.Height)
	}
	for i, r := range refs {
		fmt.Fprintf(b, "Gallery ref %d (a reference, not a copy): ", i+1)
		switch {
		case r.State == dto.RefUnavailable:
			b.WriteString("unavailable to you.\n")
		case r.State == dto.RefGone:
			b.WriteString("no longer available.\n")
		case r.Item != nil:
			fmt.Fprintf(b, "%s %q", r.Item.Ref, r.Item.Title)
			if r.Section != "" {
				fmt.Fprintf(b, " in section %q", r.Section)
			}
			fmt.Fprintf(b, ", %s.\n", show(*r.Item))
		default:
			fmt.Fprintf(b, "section %q of #%s, %d images; the newest:\n", r.Section, r.Gallery, r.Count)
			for _, it := range r.Items {
				fmt.Fprintf(b, "  %s %q, %s.\n", it.Ref, it.Title, show(it))
			}
		}
	}
}
