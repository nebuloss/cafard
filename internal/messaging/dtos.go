package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/blob"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// meta is the part of an entry's meta messaging reads and writes.
type meta struct {
	Choices *[]string `json:"choices,omitempty"`
	Choice  string    `json:"choice,omitempty"`
	Section string    `json:"section,omitempty"` // a gallery item's
}

func readMeta(e store.Entry) meta {
	var m meta
	if len(e.Meta) > 0 {
		_ = json.Unmarshal(e.Meta, &m)
	}
	return m
}

// IsQuestion reports whether a message asks the humans something.
func IsQuestion(e store.Entry) bool {
	return e.Kind == store.KindMessage && readMeta(e).Choices != nil
}

// answer is the first reply to a question from a human, if any.
func (s *Service) answer(ctx context.Context, seq int64) (*dto.Answer, error) {
	var (
		r      int64
		sender string
		m      sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `SELECT e.seq, e.sender, e.meta FROM entries e JOIN principals p ON p.name = e.sender
		WHERE e.reply_to = ? AND e.kind = 'message' AND p.kind = 'human' ORDER BY e.seq LIMIT 1`, seq).Scan(&r, &sender, &m)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a := &dto.Answer{By: sender, Seq: r}
	if m.Valid {
		var mm meta
		_ = json.Unmarshal([]byte(m.String), &mm)
		a.Choice = mm.Choice
	}
	return a, nil
}

// seenBy lists who acked a message, its sender excluded, in ack order.
func (s *Service) seenBy(ctx context.Context, e store.Entry) ([]string, error) {
	return store.QueryColumn[string](ctx, s.db, `SELECT principal FROM acks WHERE seq = ? AND principal != ? ORDER BY at, principal`, e.Seq, e.Sender)
}

// EntryDTO is e as viewer sees it in the API: chat from its side, sender
// kind, segments, attachments with URLs and log previews, receipts and the
// question state.
func (s *Service) EntryDTO(ctx context.Context, viewer auth.Principal, e store.Entry) (dto.Entry, error) {
	out := BaseDTO(e)
	if !store.IsMessageStream(e.Stream) {
		return out, nil
	}
	out.Chat = ChatID(viewer.Name, e.Stream)
	if e.Kind != store.KindMessage {
		return out, nil
	}
	out.Sender = &dto.Sender{Name: e.Sender, Kind: dto.PrincipalKind(s.kindOf(ctx, e.Sender))}
	out.Recipient = e.Recipient
	out.ReplyTo = e.ReplyTo
	out.Segments = Segments(e.Text)
	for _, a := range e.Attachments {
		switch a.Type {
		case dto.AttachmentImage:
			out.Attachments = append(out.Attachments, dto.ImageOf(ImageDTO(a)))
		case dto.AttachmentLog:
			la, err := s.logDTO(ctx, viewer, a)
			if err != nil {
				return dto.Entry{}, err
			}
			out.Attachments = append(out.Attachments, dto.LogOf(la))
		}
	}
	seen, err := s.seenBy(ctx, e)
	if err != nil {
		return dto.Entry{}, err
	}
	out.SeenBy = seen
	m := readMeta(e)
	out.Section = m.Section
	if m.Choices != nil {
		q := &dto.Question{Choices: *m.Choices, Open: true}
		if q.Answer, err = s.answer(ctx, e.Seq); err != nil {
			return dto.Entry{}, err
		}
		q.Open = q.Answer == nil
		out.Question = q
	}
	return out, nil
}

// BaseDTO is the part of an entry's DTO that needs no lookup.
func BaseDTO(e store.Entry) dto.Entry {
	out := dto.Entry{
		Seq: e.Seq, TS: dto.TimeOf(e.TS), Stream: e.Stream, Kind: dto.EntryKind(e.Kind),
		Severity: e.Severity, Host: e.Host, App: e.App, Text: e.Text, Meta: e.Meta,
	}
	if e.SrcTS != nil {
		out.SrcTS = dto.TimePtr(*e.SrcTS)
	}
	return out
}

// ImageDTO is an image attachment with its URLs.
func ImageDTO(a store.Attachment) dto.ImageAttachment {
	img := dto.ImageAttachment{SHA256: a.SHA256, Mime: a.Mime, Width: a.Width, Height: a.Height,
		URL: "/blob/" + a.SHA256, Preview: a.Mime != blob.MimeHEIC}
	if img.Preview {
		img.ThumbURL = "/blob/" + a.SHA256 + "?v=thumb"
	}
	return img
}
