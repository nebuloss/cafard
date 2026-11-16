package messaging

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// The web UI's view of chats: the list, the overview of the home page,
// the badges and the filtered timeline (PLAN 9.6.3).

// ChatsQuery selects the chat list. By default it holds the followed,
// unarchived channels and the direct conversations; All adds the
// channels the viewer unfollowed, Archived the archived ones.
type ChatsQuery struct {
	All, Archived bool
}

// Chats is the chat list in its order: pinned channels, then channels by
// last activity (messages and bound-stream events), then direct
// conversations by last activity.
func (s *Service) Chats(ctx context.Context, p auth.Principal, q ChatsQuery) ([]dto.Chat, error) {
	chs, err := s.Channels(ctx, true)
	if err != nil {
		return nil, err
	}
	var channels, direct []dto.Chat
	for _, ch := range chs {
		if ch.Archived() && !q.Archived {
			continue
		}
		c, err := s.ChannelChat(ctx, p, ch)
		if err != nil {
			return nil, err
		}
		if !c.Followed && !q.All && !c.Archived {
			continue
		}
		if err := s.eventPreview(ctx, p, &c); err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	peers, err := s.peers(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	for _, peer := range peers {
		c, err := s.DirectChat(ctx, p, peer)
		if err != nil {
			return nil, err
		}
		direct = append(direct, c)
	}
	slices.SortStableFunc(channels, func(a, b dto.Chat) int {
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		return byActivity(a, b)
	})
	slices.SortStableFunc(direct, byActivity)
	return append(channels, direct...), nil
}

func byActivity(a, b dto.Chat) int {
	ta, tb := activityOf(a), activityOf(b)
	switch {
	case ta.After(tb):
		return -1
	case tb.After(ta):
		return 1
	}
	return strings.Compare(a.Name, b.Name)
}

func activityOf(c dto.Chat) time.Time {
	if c.LastActivity == nil {
		return time.Time{}
	}
	return c.LastActivity.Time
}

// peers are the principals p has a direct conversation with.
func (s *Service) peers(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM streams WHERE id LIKE 'dm:%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		a, b, ok := DMParties(id)
		switch {
		case !ok:
		case a == name:
			out = append(out, b)
		case b == name:
			out = append(out, a)
		}
	}
	return out, rows.Err()
}

// eventPreview lets the newest event row of a bound channel stand for its
// last activity and preview when it is newer than the last message.
func (s *Service) eventPreview(ctx context.Context, p auth.Principal, c *dto.Chat) error {
	if c.Surface == dto.SurfaceNone || len(c.Streams) == 0 {
		return nil
	}
	last, err := s.st.LastSeq(ctx)
	if err != nil {
		return err
	}
	page, err := s.Timeline(ctx, p, c.ID, TimelineQuery{BeforeSeq: last + 1, Limit: 1})
	if err != nil || len(page.Rows) == 0 {
		return err
	}
	r := page.Rows[len(page.Rows)-1]
	if !r.Event || (c.Preview != nil && c.Preview.Seq > r.Seq) {
		return nil
	}
	c.Preview = &dto.ChatPreview{Seq: r.Seq, Sender: r.Stream, Text: previewText(r.Text, 140), Event: true}
	if c.LastActivity == nil || r.TS.After(c.LastActivity.Time) {
		c.LastActivity = dto.TimePtr(r.TS)
	}
	return nil
}

// questionsFor are the open questions addressed to p (by name, or to
// @humans for a human), newest first, as "Needs you" lists them. A muted
// or unfollowed chat drops its @humans questions, never the ones that
// name p.
func (s *Service) questionsFor(ctx context.Context, p auth.Principal, limit int) ([]store.Entry, error) {
	seqs, err := s.seqs(ctx, `SELECT q.seq FROM entries q WHERE q.kind = 'message'
		AND q.meta IS NOT NULL AND json_type(q.meta, '$.choices') IS NOT NULL
		AND coalesce(q.sender, '') != ?1
		AND (q.recipient = ?1 OR (q.recipient = '@humans' AND ?2 = 1))
		AND NOT EXISTS (SELECT 1 FROM entries r JOIN principals pr ON pr.name = r.sender
			WHERE r.reply_to = q.seq AND r.kind = 'message' AND pr.kind = 'human')
		ORDER BY q.seq DESC LIMIT 200`, p.Name, b2i(p.Kind == auth.KindHuman))
	if err != nil {
		return nil, err
	}
	var out []store.Entry
	for _, seq := range seqs {
		if len(out) >= limit {
			break
		}
		e, err := s.st.Get(ctx, seq)
		if err != nil || !s.Visible(ctx, p, e) {
			continue
		}
		if e.Recipient != p.Name {
			fs, err := s.Follow(ctx, p, e.Stream)
			if err != nil {
				return nil, err
			}
			if !fs.Followed || fs.Muted {
				continue
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// QuestionsFor are the questions of "Needs you", as DTOs.
func (s *Service) QuestionsFor(ctx context.Context, p auth.Principal) ([]dto.Entry, error) {
	qs, err := s.questionsFor(ctx, p, 50)
	if err != nil {
		return nil, err
	}
	out := make([]dto.Entry, 0, len(qs))
	for _, e := range qs {
		d, err := s.EntryDTO(ctx, p, e)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// recentLimit is how many entries the overview's recent activity holds.
const recentLimit = 30

// Overview is the home page's pane: what needs p, direct conversations
// with unread messages, and the latest messages and event rows across
// the chats p follows.
func (s *Service) Overview(ctx context.Context, p auth.Principal) (dto.Overview, error) {
	var o dto.Overview
	var err error
	if o.Questions, err = s.QuestionsFor(ctx, p); err != nil {
		return o, err
	}
	chats, err := s.Chats(ctx, p, ChatsQuery{})
	if err != nil {
		return o, err
	}
	last, err := s.st.LastSeq(ctx)
	if err != nil {
		return o, err
	}
	var rows []TimelineRow
	for _, c := range chats {
		if c.Kind == dto.ChatDirect && c.Unread > 0 {
			o.Direct = append(o.Direct, c)
		}
		stream, err := StreamOfChat(p.Name, c.ID)
		if err != nil {
			continue
		}
		page, err := s.Timeline(ctx, p, stream, TimelineQuery{BeforeSeq: last + 1, Limit: recentLimit})
		if err != nil {
			return o, err
		}
		rows = append(rows, page.Rows...)
	}
	slices.SortFunc(rows, func(a, b TimelineRow) int { return cmpInt(b.Seq, a.Seq) })
	rows = slices.CompactFunc(rows, func(a, b TimelineRow) bool { return a.Seq == b.Seq })
	if len(rows) > recentLimit {
		rows = rows[:recentLimit]
	}
	for _, r := range rows {
		d, err := s.EntryDTO(ctx, p, r.Entry)
		if err != nil {
			return o, err
		}
		d.Event = r.Event
		if r.Event {
			d.Chat = s.chatOfEvent(ctx, p, chats, r.Entry)
		}
		o.Recent = append(o.Recent, d)
	}
	return o, nil
}

// chatOfEvent is the first chat in the list whose bindings show e.
func (s *Service) chatOfEvent(_ context.Context, _ auth.Principal, chats []dto.Chat, e store.Entry) string {
	for _, c := range chats {
		for _, st := range c.Streams {
			if st.ID == e.Stream {
				return c.ID
			}
		}
	}
	return ""
}

// Counts are p's badges (view.Counter): open questions for p, unread
// direct messages, and each chat with something unread or open. A muted
// channel counts no unread messages.
func (s *Service) Counts(ctx context.Context, p auth.Principal) (dto.Counts, error) {
	var c dto.Counts
	qs, err := s.questionsFor(ctx, p, 1000)
	if err != nil {
		return c, err
	}
	c.OpenQuestions = len(qs)
	perChat := map[string]int{}
	for _, q := range qs {
		perChat[ChatID(p.Name, q.Stream)]++
	}
	chs, err := s.Channels(ctx, false)
	if err != nil {
		return c, err
	}
	for _, ch := range chs {
		fs, err := s.Follow(ctx, p, ch.Stream())
		if err != nil {
			return c, err
		}
		n := 0
		if fs.Followed && !fs.Muted {
			if n, err = s.unread(ctx, p, ch.Stream()); err != nil {
				return c, err
			}
		}
		if n > 0 || perChat[ch.Stream()] > 0 {
			c.Chats = append(c.Chats, dto.ChatCount{ID: ch.Stream(), Unread: n, OpenQuestions: perChat[ch.Stream()]})
		}
	}
	peers, err := s.peers(ctx, p.Name)
	if err != nil {
		return c, err
	}
	slices.Sort(peers)
	for _, peer := range peers {
		n, err := s.unread(ctx, p, DMStream(p.Name, peer))
		if err != nil {
			return c, err
		}
		c.UnreadDirect += n
		id := "dm:" + peer
		if n > 0 || perChat[id] > 0 {
			c.Chats = append(c.Chats, dto.ChatCount{ID: id, Unread: n, OpenQuestions: perChat[id]})
		}
	}
	return c, nil
}

// Present is e's DTO for viewer (view.Presenter); a lookup that fails
// leaves the parts that need none.
func (s *Service) Present(ctx context.Context, viewer auth.Principal, e store.Entry) dto.Entry {
	out, err := s.EntryDTO(ctx, viewer, e)
	if err != nil {
		out = BaseDTO(e)
		out.Chat = ChatID(viewer.Name, s.ChatOf(e))
	}
	return out
}

// Timeline filters (the chips of a chat, `f=`).
const (
	FilterAll       = "all"
	FilterQuestions = "questions"
	FilterEvents    = "events"
	FilterMine      = "mine"
	FilterImages    = "images"
)

// ValidFilter reports whether f is a timeline filter.
func ValidFilter(f string) bool {
	switch f {
	case "", FilterAll, FilterQuestions, FilterEvents, FilterMine, FilterImages:
		return true
	}
	return false
}

func keepRow(f string, viewer auth.Principal, r TimelineRow) bool {
	switch f {
	case FilterQuestions:
		return IsQuestion(r.Entry)
	case FilterEvents:
		return r.Event
	case FilterMine:
		return r.Kind == store.KindMessage && r.Sender == viewer.Name
	case FilterImages:
		for _, a := range r.Attachments {
			if a.Type == dto.AttachmentImage {
				return true
			}
		}
		return false
	}
	return true
}

// maxFilterPages bounds the pages a filtered timeline reads to fill one.
const maxFilterPages = 20

// FilteredTimeline is Timeline with a chip filter. Backward pages (the
// usual case: the newest rows, then older ones) read on until the page is
// full or the chat's start; forward pages filter what they read.
func (s *Service) FilteredTimeline(ctx context.Context, viewer auth.Principal, stream string, q TimelineQuery, f string) (TimelinePage, error) {
	if f == "" || f == FilterAll {
		return s.Timeline(ctx, viewer, stream, q)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if q.BeforeSeq == 0 {
		page, err := s.Timeline(ctx, viewer, stream, q)
		if err != nil {
			return page, err
		}
		page.Rows = slices.DeleteFunc(page.Rows, func(r TimelineRow) bool { return !keepRow(f, viewer, r) })
		return page, nil
	}
	var out TimelinePage
	before := q.BeforeSeq
	for i := range maxFilterPages {
		page, err := s.Timeline(ctx, viewer, stream, TimelineQuery{BeforeSeq: before, Limit: 200})
		if err != nil {
			return out, err
		}
		var kept []TimelineRow
		for _, r := range page.Rows {
			if keepRow(f, viewer, r) {
				kept = append(kept, r)
			}
		}
		out.Rows = append(kept, out.Rows...)
		if len(page.Rows) == 0 || !page.HasMore {
			break
		}
		before = page.Rows[0].Seq
		// Stopping with older rows unread: there may be more to show.
		if len(out.Rows) >= limit || i == maxFilterPages-1 {
			out.HasMore = true
			break
		}
	}
	if len(out.Rows) > limit {
		out.Rows = out.Rows[len(out.Rows)-limit:]
		out.HasMore = true
	}
	if last, err := s.st.LastSeq(ctx); err == nil {
		out.NextSeq = last + 1
	}
	return out, nil
}
