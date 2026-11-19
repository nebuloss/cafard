package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/store"
)

// InboxQuery selects inbox messages.
type InboxQuery struct {
	Channel      string // only this channel
	Project      string // only the channels of this project
	Limit        int
	IncludeAcked bool
}

// inboxWhere is the PLAN section 6 inbox rule for principal ?1 (kind
// human when ?2 is 1), as SQL on entries e.
const inboxWhere = `e.kind = 'message' AND coalesce(e.sender, '') != ?1
	AND (e.recipient = ?1
		OR (e.recipient = '@humans' AND ?2 = 1)
		OR (e.recipient IS NULL AND e.stream LIKE 'chan:%' AND (
			(?2 = 1 AND NOT EXISTS (SELECT 1 FROM follows f WHERE f.principal = ?1 AND f.chat = e.stream AND f.followed = 0))
			OR (?2 = 0 AND EXISTS (SELECT 1 FROM follows f WHERE f.principal = ?1 AND f.chat = e.stream AND f.followed = 1)))))`

const notAcked = ` AND NOT EXISTS (SELECT 1 FROM acks a WHERE a.principal = ?1 AND a.seq = e.seq)`

// readableWhere keeps direct messages (the inbox rule only lets in those
// of p) and messages of the channels in the JSON array bound at ?n.
func readableWhere(n int) string {
	return fmt.Sprintf(` AND (e.stream LIKE 'dm:%%' OR e.stream IN (SELECT value FROM json_each(?%d)))`, n)
}

// readableChannels is the JSON array of the message streams of the
// channels p may read, of one project when project is set.
func (s *Service) readableChannels(ctx context.Context, p auth.Principal, project string) (string, error) {
	chs, err := s.Channels(ctx, p, true)
	if err != nil {
		return "", err
	}
	ids := []string{}
	for _, ch := range chs {
		if project == "" || ch.Project() == project {
			ids = append(ids, ch.Stream())
		}
	}
	b, err := json.Marshal(ids)
	return string(b), err
}

// Inbox returns p's inbox, oldest first. With IncludeAcked it returns the
// latest messages, acked or not.
func (s *Service) Inbox(ctx context.Context, p auth.Principal, q InboxQuery) ([]store.Entry, bool, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 200)
	readable, err := s.readableChannels(ctx, p, q.Project)
	if err != nil {
		return nil, false, err
	}
	where := inboxWhere + readableWhere(3)
	args := []any{p.Name, b2i(p.Kind == auth.KindHuman), readable}
	if !q.IncludeAcked {
		where += notAcked
	}
	if q.Project != "" {
		where += ` AND e.stream NOT LIKE 'dm:%'`
	}
	if q.Channel != "" {
		ch, err := s.Channel(ctx, p, q.Channel)
		if err != nil {
			return nil, false, err
		}
		where += ` AND e.stream = ?4`
		args = append(args, ch.Stream())
	}
	order := `ASC`
	if q.IncludeAcked {
		order = `DESC`
	}
	seqs, err := store.QueryColumn[int64](ctx, s.db, `SELECT e.seq FROM entries e WHERE `+where+` ORDER BY e.seq `+order+fmt.Sprintf(` LIMIT %d`, limit+1), args...)
	if err != nil {
		return nil, false, err
	}
	more := len(seqs) > limit
	if more {
		seqs = seqs[:limit]
	}
	if q.IncludeAcked {
		slices.Reverse(seqs)
	}
	out := make([]store.Entry, 0, len(seqs))
	for _, seq := range seqs {
		e, err := s.st.Get(ctx, seq)
		if err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, more, nil
}

// inInbox reports whether a committed message is in p's inbox, acks
// aside.
func (s *Service) inInbox(ctx context.Context, p auth.Principal, e store.Entry) (bool, error) {
	if e.Kind != store.KindMessage || !s.Visible(ctx, p, e) {
		return false, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM entries e WHERE e.seq = ?3 AND `+inboxWhere,
		p.Name, b2i(p.Kind == auth.KindHuman), e.Seq).Scan(&n)
	return n > 0, err
}

// Ack marks messages read by p and returns how many were newly acked.
// Seqs p cannot see, or that are not messages, are skipped.
func (s *Service) Ack(ctx context.Context, p auth.Principal, seqs []int64) (int, error) {
	n := 0
	now := s.clock.Now().UnixNano()
	for _, seq := range seqs {
		e, err := s.st.Get(ctx, seq)
		if err != nil || e.Kind != store.KindMessage || !s.Visible(ctx, p, e) {
			continue
		}
		r, err := s.db.ExecContext(ctx, `INSERT INTO acks(principal, seq, at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, p.Name, seq, now)
		if err != nil {
			return n, err
		}
		if k, _ := r.RowsAffected(); k > 0 {
			n++
			s.emit(ChatEvent{Kind: EventSeen, Stream: e.Stream, Seq: seq, By: p.Name})
		}
	}
	return n, nil
}

// AckUpTo acks every message of a chat up to seq (the web UI's "read").
func (s *Service) AckUpTo(ctx context.Context, p auth.Principal, stream string, upTo int64) (int, error) {
	seqs, err := store.QueryColumn[int64](ctx, s.db, `SELECT e.seq FROM entries e WHERE e.stream = ? AND e.kind = 'message' AND e.seq <= ?
		AND NOT EXISTS (SELECT 1 FROM acks a WHERE a.principal = ? AND a.seq = e.seq)`, stream, upTo, p.Name)
	if err != nil {
		return 0, err
	}
	return s.Ack(ctx, p, seqs)
}

// WaitQuery is what wait_message waits for.
type WaitQuery struct {
	From    string // sender
	Channel string
	ReplyTo int64 // replies to this message, from anyone visible; otherwise inbox messages
	Timeout time.Duration
	// HumansOnly keeps replies from human principals (ask_human).
	HumansOnly bool
	// Progress is called every 10 s while waiting.
	Progress func(elapsed time.Duration)
}

const (
	waitSubBuffer = 1024
	progressEvery = 10 * time.Second
)

func (s *Service) waitMatches(ctx context.Context, p auth.Principal, q WaitQuery, stream string, e store.Entry) (bool, error) {
	if e.Kind != store.KindMessage || e.Sender == p.Name || !s.Visible(ctx, p, e) {
		return false, nil
	}
	if q.From != "" && e.Sender != q.From {
		return false, nil
	}
	if stream != "" && e.Stream != stream {
		return false, nil
	}
	if q.HumansOnly && s.kindOf(ctx, e.Sender) != auth.KindHuman {
		return false, nil
	}
	if q.ReplyTo != 0 {
		return e.ReplyTo == q.ReplyTo, nil
	}
	return s.inInbox(ctx, p, e)
}

// Wait returns the first message matching q that p has not acked: one
// already stored, or the next to arrive. It subscribes before reading
// the store, so nothing committed in between is missed.
func (s *Service) Wait(ctx context.Context, p auth.Principal, q WaitQuery) (store.Entry, bool, error) {
	stream := ""
	if q.Channel != "" {
		ch, err := s.Channel(ctx, p, q.Channel)
		if err != nil {
			return store.Entry{}, false, err
		}
		stream = ch.Stream()
	}
	filter := hub.Filter{Streams: s.access(ctx, p).MessagePatterns(), Kinds: []store.Kind{store.KindMessage}, MinSeverity: 7, Viewer: &p}
	sub := s.hub.Subscribe(filter, waitSubBuffer)
	defer func() { sub.Close() }()

	deadline := time.NewTimer(q.Timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(progressEvery)
	defer ticker.Stop()
	started := time.Now()

	var cursor int64 // highest seq considered
	for {
		e, found, last, err := s.waitStored(ctx, p, q, stream)
		if err != nil || found {
			return e, found, err
		}
		cursor = max(cursor, last)
	drain:
		for {
			select {
			case e := <-sub.C():
				if e.Seq <= cursor {
					continue
				}
				cursor = e.Seq
				ok, err := s.waitMatches(ctx, p, q, stream, e)
				if err != nil {
					return store.Entry{}, false, err
				}
				if ok && !s.acked(ctx, p.Name, e.Seq) {
					return e, true, nil
				}
			case <-sub.Lagged():
				sub.Close()
				sub = s.hub.Subscribe(filter, waitSubBuffer)
				break drain
			case <-ticker.C:
				if q.Progress != nil {
					q.Progress(time.Since(started))
				}
			case <-deadline.C:
				return store.Entry{}, false, nil
			case <-ctx.Done():
				return store.Entry{}, false, ctx.Err()
			}
		}
	}
}

func (s *Service) acked(ctx context.Context, principal string, seq int64) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM acks WHERE principal = ? AND seq = ?`, principal, seq).Scan(&n)
	return n > 0
}

// waitStored finds a stored match; last is the newest seq it covered.
func (s *Service) waitStored(ctx context.Context, p auth.Principal, q WaitQuery, stream string) (store.Entry, bool, int64, error) {
	last, err := s.st.LastSeq(ctx)
	if err != nil {
		return store.Entry{}, false, 0, err
	}
	readable, err := s.readableChannels(ctx, p, "")
	if err != nil {
		return store.Entry{}, false, 0, err
	}
	var (
		where strings.Builder
		args  = []any{p.Name, b2i(p.Kind == auth.KindHuman), readable}
	)
	where.WriteString(`e.kind = 'message' AND coalesce(e.sender, '') != ?1` + notAcked + readableWhere(3))
	if q.ReplyTo != 0 {
		where.WriteString(fmt.Sprintf(` AND e.reply_to = %d`, q.ReplyTo))
	} else {
		where.WriteString(` AND ` + inboxWhere)
	}
	if q.From != "" {
		args = append(args, q.From)
		where.WriteString(fmt.Sprintf(` AND e.sender = ?%d`, len(args)))
	}
	if stream != "" {
		args = append(args, stream)
		where.WriteString(fmt.Sprintf(` AND e.stream = ?%d`, len(args)))
	}
	where.WriteString(fmt.Sprintf(` AND e.seq <= %d`, last))
	seqs, err := store.QueryColumn[int64](ctx, s.db, `SELECT e.seq FROM entries e WHERE `+where.String()+` ORDER BY e.seq LIMIT 200`, args...)
	if err != nil {
		return store.Entry{}, false, 0, err
	}
	for _, seq := range seqs {
		e, err := s.st.Get(ctx, seq)
		if err != nil {
			continue
		}
		ok, err := s.waitMatches(ctx, p, q, stream, e)
		if err != nil {
			return store.Entry{}, false, 0, err
		}
		if ok {
			return e, true, last, nil
		}
	}
	return store.Entry{}, false, last, nil
}

// AskResult is the outcome of AskHuman.
type AskResult struct {
	Question store.Entry
	Reply    *store.Entry
	Choice   string
}

// AskHuman posts a question to the humans and waits up to timeout for
// the first human reply. A zero timeout returns at once.
func (s *Service) AskHuman(ctx context.Context, p auth.Principal, question string, choices []string, channel string, timeout time.Duration, progress func(time.Duration)) (AskResult, error) {
	if strings.TrimSpace(question) == "" {
		return AskResult{}, dto.Errorf(dto.CodeBadRequest, "ask_human needs a question.")
	}
	if choices == nil {
		choices = []string{}
	}
	q, err := s.SendMessage(ctx, p, SendInput{SendRequest: dto.SendRequest{Text: question, To: dto.RecipientHumans, Channel: channel}, Choices: choices})
	if err != nil {
		return AskResult{}, err
	}
	res := AskResult{Question: q}
	if timeout <= 0 {
		return res, nil
	}
	reply, ok, err := s.Wait(ctx, p, WaitQuery{ReplyTo: q.Seq, Timeout: timeout, HumansOnly: true, Progress: progress})
	if err != nil || !ok {
		return res, err
	}
	res.Reply = &reply
	res.Choice = readMeta(reply).Choice
	return res, nil
}
