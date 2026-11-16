package messaging

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/blob"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// Message limits (PLAN section 6, Guards).
const (
	MaxTextBytes    = 32 << 10
	MaxAgentImages  = 6
	MaxAgentImageMB = 10
	MaxChoices      = 10
	maxChoiceLen    = 200
)

// SendInput is a SendRequest plus what only the server side may set.
type SendInput struct {
	dto.SendRequest
	// Blobs are files already stored (multipart uploads).
	Blobs []dto.Blob
	// Choices makes the message a question (ask_human); non-nil even when
	// empty for a free-text question.
	Choices []string
}

// Send implements deps.Messaging.
func (s *Service) Send(ctx context.Context, from auth.Principal, m dto.SendRequest) (dto.Entry, error) {
	e, err := s.SendMessage(ctx, from, SendInput{SendRequest: m})
	if err != nil {
		return dto.Entry{}, err
	}
	return s.EntryDTO(ctx, from, e)
}

// target is where a message goes.
type target struct {
	stream    string
	recipient string
	channel   string // set for channel messages
}

func (s *Service) target(ctx context.Context, from auth.Principal, to, channel string) (target, error) {
	switch {
	case to != "" && to != dto.RecipientHumans:
		to = strings.TrimPrefix(to, "@")
		if to == from.Name {
			return target{}, dto.Errorf(dto.CodeBadRequest, "You cannot send a direct message to yourself.")
		}
		if !s.principalExists(ctx, to) {
			return target{}, dto.Errorf(dto.CodeNotFound, "There is no principal %q; list_principals shows who can be messaged.", to)
		}
		return target{stream: DMStream(from.Name, to), recipient: to}, nil
	default:
		if channel == "" {
			channel = General
		}
		ch, err := s.Channel(ctx, channel)
		if err != nil {
			return target{}, err
		}
		t := target{stream: ch.Stream(), channel: ch.Name}
		if to == dto.RecipientHumans {
			t.recipient = dto.RecipientHumans
		}
		return t, nil
	}
}

// SendMessage validates and commits a message, and returns it committed.
// The sender is always from, the authenticated principal.
func (s *Service) SendMessage(ctx context.Context, from auth.Principal, in SendInput) (store.Entry, error) {
	if err := needMessage(from); err != nil {
		return store.Entry{}, err
	}
	m := in.SendRequest
	if len(m.Text) > MaxTextBytes {
		return store.Entry{}, dto.Errorf(dto.CodeTooLarge, "The message text is %d KiB; the limit is %d KiB.", (len(m.Text)+1023)>>10, MaxTextBytes>>10)
	}
	if !utf8.ValidString(m.Text) {
		return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "The message text is not valid UTF-8.")
	}
	if len(m.Images) > MaxAgentImages {
		return store.Entry{}, dto.Errorf(dto.CodeTooLarge, "A message carries at most %d inline images.", MaxAgentImages)
	}
	if n := len(m.Images) + len(in.Blobs); n > s.opts.Images.MaxFiles {
		return store.Entry{}, dto.Errorf(dto.CodeTooLarge, "A message carries at most %d files; this one has %d.", s.opts.Images.MaxFiles, n)
	}
	if len(m.Logs) > MaxLogAttachments {
		return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "A message carries at most %d log attachments.", MaxLogAttachments)
	}
	if len(in.Choices) > MaxChoices {
		return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "A question has at most %d choices.", MaxChoices)
	}
	for _, c := range in.Choices {
		if strings.TrimSpace(c) == "" || len(c) > maxChoiceLen {
			return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "A choice must be 1 to %d bytes.", maxChoiceLen)
		}
	}
	t, err := s.target(ctx, from, m.To, m.Channel)
	if err != nil {
		return store.Entry{}, err
	}

	var md meta
	replyTo := m.ReplyTo
	if replyTo != 0 {
		parent, err := s.Message(ctx, from, replyTo)
		if err != nil {
			return store.Entry{}, dto.Errorf(dto.CodeNotFound, "reply_to %d is not a message you can see.", replyTo)
		}
		if parent.Kind != store.KindMessage {
			return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "reply_to %d is not a message.", replyTo)
		}
		if m.Choice != "" {
			pm := readMeta(parent)
			if pm.Choices == nil {
				return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "Message %d is not a question, so it takes no choice.", replyTo)
			}
			if !slices.Contains(*pm.Choices, m.Choice) {
				return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "%q is not one of the choices of message %d: %s.", m.Choice, replyTo, strings.Join(*pm.Choices, ", "))
			}
			md.Choice = m.Choice
		}
		// Threads are flat: a reply to a reply points at the root.
		if parent.ReplyTo != 0 {
			replyTo = parent.ReplyTo
		}
	} else if m.Choice != "" {
		return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "choice answers a question: set reply_to to its seq.")
	}
	text := m.Text
	if strings.TrimSpace(text) == "" && md.Choice != "" {
		text = md.Choice
	}

	blobs := slices.Clone(in.Blobs)
	if m.Staged != "" {
		s.mu.Lock()
		lookup := s.staged
		s.mu.Unlock()
		if lookup == nil {
			return store.Entry{}, dto.Errorf(dto.CodeNotFound, "There is no staged share %q.", m.Staged)
		}
		staged, err := lookup(ctx, from, m.Staged)
		if err != nil {
			return store.Entry{}, err
		}
		blobs = append(blobs, staged...)
	}
	if strings.TrimSpace(text) == "" && len(m.Images) == 0 && len(blobs) == 0 && len(m.Logs) == 0 {
		return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "A message needs text, an image or a log attachment.")
	}

	if ok, wait := s.sendLimit.take(from.Name); !ok {
		sec := retrySeconds(wait)
		return store.Entry{}, dto.Errorf(dto.CodeRateLimited,
			"You sent more than %d messages in a short time (the limit is %d per minute); wait %d s. Do not reply to acknowledgements.",
			s.opts.Config.Burst, s.opts.Config.RatePerMinute, sec).RetryAfter(sec)
	}

	var atts []store.Attachment
	for i, img := range m.Images {
		b, err := s.putInline(ctx, i, img)
		if err != nil {
			return store.Entry{}, err
		}
		blobs = append(blobs, b)
	}
	for _, b := range blobs {
		if !blob.IsImage(b.Mime) {
			return store.Entry{}, dto.Errorf(dto.CodeBadRequest, "Only images can be attached as files.")
		}
		atts = append(atts, store.Attachment{Type: dto.AttachmentImage, SHA256: b.SHA256, Mime: b.Mime, Width: b.Width, Height: b.Height})
	}
	for i, lr := range m.Logs {
		a, err := s.buildLogAttachment(ctx, i, lr)
		if err != nil {
			return store.Entry{}, err
		}
		atts = append(atts, a)
	}
	if in.Choices != nil {
		c := slices.Clone(in.Choices)
		md.Choices = &c
	}
	var rawMeta json.RawMessage
	if md.Choices != nil || md.Choice != "" {
		if rawMeta, err = json.Marshal(md); err != nil {
			return store.Entry{}, err
		}
	}

	e := store.Entry{
		Stream: t.stream, Kind: store.KindMessage, Severity: store.DefaultSeverity, Text: text,
		Attachments: atts, Sender: from.Name, Recipient: t.recipient, ReplyTo: replyTo, Meta: rawMeta,
	}
	seq, err := s.w.Commit(ctx, e)
	if err != nil {
		return store.Entry{}, err
	}
	if t.channel != "" {
		if err := s.touchChannel(ctx, t.channel); err != nil {
			return store.Entry{}, err
		}
		if from.Kind == auth.KindAgent {
			if err := s.autoFollow(ctx, from.Name, t.stream); err != nil {
				return store.Entry{}, err
			}
		}
	}
	committed, err := s.st.Get(ctx, seq)
	if err != nil {
		return store.Entry{}, err
	}
	return committed, nil
}

func (s *Service) putInline(ctx context.Context, i int, img dto.ImageUpload) (dto.Blob, error) {
	limit := MaxAgentImageMB << 20
	if base64.StdEncoding.DecodedLen(len(img.Data)) > limit+3 {
		return dto.Blob{}, dto.Errorf(dto.CodeTooLarge, "Image %d is larger than %d MiB, the limit for inline images.", i+1, MaxAgentImageMB)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(img.Data))
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(img.Data)); err != nil {
			return dto.Blob{}, dto.Errorf(dto.CodeBadRequest, "Image %d is not valid base64.", i+1)
		}
	}
	if len(data) > limit {
		return dto.Blob{}, dto.Errorf(dto.CodeTooLarge, "Image %d is larger than %d MiB, the limit for inline images.", i+1, MaxAgentImageMB)
	}
	b, err := s.blobs.Put(ctx, bytes.NewReader(data), img.Mime)
	var apiErr *dto.APIError
	if errors.As(err, &apiErr) {
		return dto.Blob{}, dto.Errorf(apiErr.Code, "Image %d: %s", i+1, apiErr.Message)
	}
	if err != nil {
		return dto.Blob{}, fmt.Errorf("storing image %d: %w", i+1, err)
	}
	return b, nil
}
