package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cafard/internal/service/access"
	"cafard/internal/store"
)

// StartConversation starts a conversation from its first message, the way
// the chat app does (docs/design.md, "The chat app": no form, the title
// comes from the first message). title may be empty; it is then the first
// words of text. The address is made from the title, and made unique.
func (s *Service) StartConversation(ctx context.Context, who access.Who, project, title, text string) (Sent, error) {
	text = strings.TrimSpace(text)
	title = strings.TrimSpace(title)
	if title == "" {
		title = TitleFrom(text)
	}
	if title == "" {
		return Sent{}, &access.Refusal{Code: access.BadValue, Message: "Write a first message, or give the conversation a title."}
	}
	if len(text) > MaxText {
		return Sent{}, &access.Refusal{Code: access.BadValue, Message: "The message is too long. Send long text as a file."}
	}
	p, err := s.project(ctx, project)
	if err != nil {
		return Sent{}, err
	}
	if err := s.acc.CanPost(ctx, who, p); err != nil {
		return Sent{}, err
	}
	base := Slug(title)
	var c store.Conversation
	for i := 1; i <= 50; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		c, err = s.st.CreateConversation(ctx, p.ID, name, title, who.ID)
		if !errors.Is(err, store.ErrExists) {
			break
		}
	}
	if err != nil {
		return Sent{}, err
	}
	sent := Sent{Project: p, Conversation: c, Created: true}
	if text != "" {
		if sent.Message, err = s.st.AddMessage(ctx, c.ID, who.ID, text, nil); err != nil {
			return Sent{}, err
		}
	}
	return sent, nil
}

// TitleFrom is a conversation's first title: the first words of its first
// message, at most 7 of them and 80 bytes, without a final full stop.
func TitleFrom(text string) string {
	words := strings.Fields(strings.ReplaceAll(text, "`", ""))
	if len(words) > 7 {
		words = words[:7]
	}
	t := strings.Join(words, " ")
	for len(t) > 80 {
		_, size := utf8.DecodeLastRuneInString(t)
		t = t[:len(t)-size]
	}
	return strings.TrimRight(t, ".,:;!? ")
}

// folds turns the accented letters of French and its neighbours into the
// letters an address is made of.
var folds = map[rune]string{
	'à': "a", 'â': "a", 'ä': "a", 'á': "a", 'ã': "a", 'å': "a", 'ç': "c", 'é': "e", 'è': "e", 'ê': "e", 'ë': "e",
	'î': "i", 'ï': "i", 'í': "i", 'ì': "i", 'ô': "o", 'ö': "o", 'ó': "o", 'ò': "o", 'õ': "o", 'ù': "u", 'û': "u",
	'ü': "u", 'ú': "u", 'ÿ': "y", 'ñ': "n", 'œ': "oe", 'æ': "ae", 'ß': "ss",
}

// Slug is a conversation's address from its title: "Wi-Fi drops after the
// 2nd reboot" is wifi-drops-after-the-2nd-reboot, at most 48 bytes, cut at a
// word.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if f, ok := folds[r]; ok {
			b.WriteString(f)
			dash = false
			continue
		}
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == '-' && b.Len() > 0 && !dash:
			// "Wi-Fi" stays one word.
		case unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r):
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 48 {
		s = s[:48]
		if i := strings.LastIndexByte(s, '-'); i > 16 {
			s = s[:i]
		}
	}
	if s == "" {
		s = "conversation"
	}
	return s
}

// AttachFile adds a file to a message who just sent, so a caption and
// several photos are one message. Only to who's own message, in the same
// conversation, within attachWindow of it.
func (s *Service) AttachFile(ctx context.Context, who access.Who, project, conversation string, messageID int64, name, contentType string, body io.Reader) (Sent, error) {
	name, err := fileName(name)
	if err != nil {
		return Sent{}, err
	}
	p, err := s.project(ctx, project)
	if err != nil {
		return Sent{}, err
	}
	if err := s.acc.CanPost(ctx, who, p); err != nil {
		return Sent{}, err
	}
	c, err := s.st.ConversationByName(ctx, p.ID, conversation)
	if err != nil {
		return Sent{}, &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No conversation %q in %s.", conversation, p.Name)}
	}
	m, err := s.st.MessageInfo(ctx, messageID)
	if err != nil || m.ConversationID != c.ID || m.AuthorID != who.ID || time.Since(m.CreatedAt) > attachWindow {
		return Sent{}, &access.Refusal{Code: access.NotAllowed, Message: "A file is added only to your own message, sent in the last few minutes, in this conversation. Send it on its own instead."}
	}
	head, rest := firstChunk(body)
	f, err := s.st.AddFile(ctx, m.ID, store.NewFile{Name: name, ContentType: detectType(name, contentType, head)})
	if err != nil {
		return Sent{}, err
	}
	stored, copyErr := s.upload(ctx, f, head, rest)
	if stored.ID == 0 {
		return Sent{}, copyErr
	}
	return Sent{Project: p, Conversation: c, Message: store.Message{ID: m.ID, ConversationID: c.ID, Author: who.Identity, CreatedAt: m.CreatedAt, Files: []store.File{stored}}}, copyErr
}

// attachWindow is how long after a message its sender may add files to it:
// enough for a phone to upload several photos one after another.
const attachWindow = 10 * time.Minute

// UpdateConversation renames, closes or reopens a conversation. Any member
// who may post may do either.
func (s *Service) UpdateConversation(ctx context.Context, who access.Who, project, conversation string, title *string, closed *bool) (store.Conversation, error) {
	p, err := s.project(ctx, project)
	if err != nil {
		return store.Conversation{}, err
	}
	if err := s.acc.CanPost(ctx, who, p); err != nil {
		return store.Conversation{}, err
	}
	c, err := s.st.ConversationByName(ctx, p.ID, conversation)
	if err != nil {
		return store.Conversation{}, &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No conversation %q in %s.", conversation, p.Name)}
	}
	if title != nil {
		t := strings.TrimSpace(*title)
		if t == "" || len(t) > 120 || !utf8.ValidString(t) {
			return c, &access.Refusal{Code: access.BadValue, Message: "A title is 1 to 120 characters."}
		}
		if err := s.st.SetConversationTitle(ctx, c.ID, t); err != nil {
			return c, err
		}
	}
	if closed != nil {
		if err := s.st.SetConversationClosed(ctx, c.ID, *closed); err != nil {
			return c, err
		}
	}
	return s.st.Conversation(ctx, c.ID)
}

// Members lists a project's members, for who is in it.
func (s *Service) Members(ctx context.Context, who access.Who, project string) ([]store.Identity, error) {
	p, err := s.readable(ctx, who, project)
	if err != nil {
		return nil, err
	}
	return s.st.Members(ctx, p.ID)
}

// AddMember puts an existing person or agent in a project, from the chat:
// service/access decides (only people, only within what they have).
func (s *Service) AddMember(ctx context.Context, who access.Who, project, name string) error {
	p, err := s.project(ctx, project)
	if err != nil {
		return err
	}
	target, err := s.st.IdentityByName(ctx, name)
	if err != nil {
		return &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No member %q.", name)}
	}
	return s.acc.AddMember(ctx, who, p, target)
}

// RemoveMember takes someone out of a project, on the same terms.
func (s *Service) RemoveMember(ctx context.Context, who access.Who, project, name string) error {
	p, err := s.project(ctx, project)
	if err != nil {
		return err
	}
	target, err := s.st.IdentityByName(ctx, name)
	if err != nil {
		return &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No member %q.", name)}
	}
	return s.acc.RemoveMember(ctx, who, p, target)
}

// Directory is the people and agents a person can add to a project from the
// chat: everyone who exists and is not removed. Devices are added from the
// admin UI, with their token.
func (s *Service) Directory(ctx context.Context, who access.Who) ([]store.Identity, error) {
	if who.Kind != store.Person {
		return nil, &access.Refusal{Code: access.NotAllowed, Message: "Only people add members."}
	}
	ids, err := s.st.Identities(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Identity
	for _, i := range ids {
		if !i.Deleted() && i.Kind != store.Device {
			out = append(out, i)
		}
	}
	return out, nil
}

// Unread counts, per conversation id, what who has not been shown in a
// project.
func (s *Service) Unread(ctx context.Context, who access.Who, project string) (map[int64]int, error) {
	p, err := s.readable(ctx, who, project)
	if err != nil {
		return nil, err
	}
	return s.st.Unread(ctx, who.ID, p.ID)
}

// Reads is who has read a conversation, and how far.
func (s *Service) Reads(ctx context.Context, who access.Who, project, conversation string) ([]store.Read, error) {
	p, err := s.readable(ctx, who, project)
	if err != nil {
		return nil, err
	}
	c, err := s.st.ConversationByName(ctx, p.ID, conversation)
	if err != nil {
		return nil, &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No conversation %q in %s.", conversation, p.Name)}
	}
	return s.st.Reads(ctx, c.ID)
}

// DeleteFile deletes a file: its sender or an admin. The message stays, and
// says the file was deleted; the stored bytes go unless another message
// still holds the same file (docs/design.md, "Files").
func (s *Service) DeleteFile(ctx context.Context, who access.Who, id int64) error {
	fw, rd, err := s.File(ctx, who, id)
	if err != nil && !access.Is(err, access.SendOnly) {
		return err
	}
	if rd != nil {
		rd.Close()
	}
	if access.Is(err, access.SendOnly) {
		// A device cannot read, but may delete what it sent: find the file
		// without the read check, and let CanDeleteFile decide.
		if fw, err = s.st.File(ctx, id); err != nil {
			return &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No file %d.", id)}
		}
	}
	if fw.State == store.Live {
		return &access.Refusal{Code: access.BadValue, Message: fmt.Sprintf("%s is still arriving. Delete it once the upload ends.", fw.Name)}
	}
	author, err := s.st.FileAuthor(ctx, id)
	if err != nil {
		return err
	}
	if err := s.acc.CanDeleteFile(ctx, who, author); err != nil {
		return err
	}
	c, err := s.st.Conversation(ctx, fw.ConversationID)
	if err != nil {
		return err
	}
	p, err := s.st.Project(ctx, fw.ProjectID)
	if err != nil {
		return err
	}
	sum, stillUsed, err := s.st.DeleteFile(ctx, id, store.AuditEntry{Actor: who.Name, Action: "deleted the file " + fw.Name + " in",
		Target: p.Name + "/" + c.Name, Origin: who.Origin})
	if errors.Is(err, store.ErrNotFound) {
		return &access.Refusal{Code: Gone, Message: fmt.Sprintf("%s is already gone.", fw.Name)}
	}
	if err != nil {
		return err
	}
	if !stillUsed && sum != "" {
		s.bl.Remove(sum)
	}
	return nil
}

// fileName checks the name a file is sent under.
func fileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || name == "." || name == ".." || name == "-" || len(name) > 255 || !utf8.ValidString(name) {
		return "", &access.Refusal{Code: access.BadName, Message: "Name the file at the end of the address: …/send/<project>/<conversation>/boot.log."}
	}
	return name, nil
}
