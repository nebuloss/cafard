// Package chat is conversations, messages and files: sending them and reading
// them, every step asked of service/access first.
//
// A conversation is created by the first thing sent to it. Nothing creates
// one empty: `curl -T boot.log …/send/be98/wifi-drops/` is how a device
// starts a conversation, and a person in the chat app does the same by
// sending its first message.
//
// A file is received as it arrives. Its message is posted first, with the
// file live, so readers see "serial-console.log · live" and can read what has
// come; the bytes are hashed on the way and become an ordinary blob when the
// upload ends. That is what makes `logread -f | curl -T - …` stream into a
// conversation with no client of cafard's own.
package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cafard/internal/blobs"
	"cafard/internal/service/access"
	"cafard/internal/store"
)

type Service struct {
	st  *store.Store
	bl  *blobs.Store
	acc *access.Service
}

func New(st *store.Store, bl *blobs.Store, acc *access.Service) *Service {
	return &Service{st: st, bl: bl, acc: acc}
}

// Limits.
const (
	// MaxText is the longest message text. A message is something a person
	// reads; a log belongs in a file, where it can be paged.
	MaxText = 64 << 10
	// growEvery is how often a live file's size is written while it
	// arrives, so a reader's view of it is never more than this stale.
	growEvery = time.Second
)

var convName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Start runs once when the server starts: uploads that were live when it
// stopped did not finish, and their staged bytes are gone.
func (s *Service) Start(ctx context.Context) (cut int64, err error) {
	return s.st.CutLiveFiles(ctx)
}

// project is a project by name, as a refusal a caller can show if there is
// none.
func (s *Service) project(ctx context.Context, name string) (store.Project, error) {
	p, err := s.st.ProjectByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Project{}, &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No project %q.", name)}
	}
	return p, err
}

// Sent is where something sent went.
type Sent struct {
	Project      store.Project
	Conversation store.Conversation
	Created      bool
	Message      store.Message
}

// conversationFor finds the conversation, or creates it, for something who
// is sending.
func (s *Service) conversationFor(ctx context.Context, who access.Who, project, conversation string) (store.Project, store.Conversation, bool, error) {
	p, err := s.project(ctx, project)
	if err != nil {
		return p, store.Conversation{}, false, err
	}
	if err := s.acc.CanPost(ctx, who, p); err != nil {
		return p, store.Conversation{}, false, err
	}
	if !convName.MatchString(conversation) {
		return p, store.Conversation{}, false, &access.Refusal{Code: access.BadName, Message: fmt.Sprintf(
			"%q is not a conversation name cafard accepts: use lowercase letters, digits and hyphens, like wifi-drops.", conversation)}
	}
	c, created, err := s.st.EnsureConversation(ctx, p.ID, conversation, titleFrom(conversation), who.ID)
	return p, c, created, err
}

// titleFrom is the first title of a conversation started by name:
// wifi-drops reads as "wifi drops" until someone renames it.
func titleFrom(name string) string {
	t := strings.ReplaceAll(name, "-", " ")
	return strings.ToUpper(t[:1]) + t[1:]
}

// SendText posts text to a conversation.
func (s *Service) SendText(ctx context.Context, who access.Who, project, conversation, text string) (Sent, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return Sent{}, &access.Refusal{Code: access.BadValue, Message: "The message is empty."}
	case len(text) > MaxText:
		return Sent{}, &access.Refusal{Code: access.BadValue, Message: fmt.Sprintf(
			"The message is %d bytes; the most is %d. Send long text as a file, where it can be read by page.", len(text), MaxText)}
	case !utf8.ValidString(text):
		return Sent{}, &access.Refusal{Code: access.BadValue, Message: "The message is not UTF-8 text. Send it as a file."}
	}
	p, c, created, err := s.conversationFor(ctx, who, project, conversation)
	if err != nil {
		return Sent{}, err
	}
	m, err := s.st.AddMessage(ctx, c.ID, who.ID, text, nil)
	if err != nil {
		return Sent{}, err
	}
	return Sent{Project: p, Conversation: c, Created: created, Message: m}, nil
}

// SendFile posts a file to a conversation, reading body until it ends. The
// message is visible, with the file live, from the moment the upload starts.
//
// If body fails partway (the sender went away), what arrived is kept and the
// file stored as it is: half a log is still the log up to where it broke,
// which is often the part that matters. The error is returned all the same.
func (s *Service) SendFile(ctx context.Context, who access.Who, project, conversation, name, contentType, text string, body io.Reader) (Sent, error) {
	name, err := fileName(name)
	if err != nil {
		return Sent{}, err
	}
	if len(text) > MaxText {
		return Sent{}, &access.Refusal{Code: access.BadValue, Message: "The caption is too long."}
	}
	p, c, created, err := s.conversationFor(ctx, who, project, conversation)
	if err != nil {
		return Sent{}, err
	}

	// The type is guessed from the first chunk, whatever its size, and not
	// from a full 512 bytes: a live log may take minutes to send that many,
	// and its message should appear with its first line.
	head, rest := firstChunk(body)
	ctype := detectType(name, contentType, head)

	m, err := s.st.AddMessage(ctx, c.ID, who.ID, strings.TrimSpace(text), []store.NewFile{{Name: name, ContentType: ctype}})
	if err != nil {
		return Sent{}, err
	}
	stored, copyErr := s.upload(ctx, m.Files[0], head, rest)
	if stored.ID == 0 {
		return Sent{}, copyErr
	}
	m.Files[0] = stored
	return Sent{Project: p, Conversation: c, Created: created, Message: m}, copyErr
}

// upload receives the bytes of a file already posted as live, and stores
// them. If the body fails partway, what arrived is stored and the error
// returned with the stored file; a zero file means nothing could be stored.
func (s *Service) upload(ctx context.Context, f store.File, head []byte, rest io.Reader) (store.File, error) {
	w, err := s.bl.Create(strconv.FormatInt(f.ID, 10))
	if err != nil {
		return store.File{}, err
	}
	copyErr := s.receive(ctx, f.ID, w, io.MultiReader(bytes.NewReader(head), rest))
	sum, size, err := w.Commit()
	if err != nil {
		return store.File{}, err
	}
	if err := s.st.StoreFile(context.WithoutCancel(ctx), f.ID, sum, size); err != nil {
		return store.File{}, err
	}
	f.State, f.SHA256, f.Size = store.Stored, sum, size
	return f, copyErr
}

// receive copies body into w, telling the store how far the file has got at
// most once every growEvery, so a reader of a live file sees it grow.
func (s *Service) receive(ctx context.Context, fileID int64, w *blobs.Writer, body io.Reader) error {
	buf := make([]byte, 32<<10)
	last := time.Now()
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if time.Since(last) >= growEvery {
				s.st.GrowFile(context.WithoutCancel(ctx), fileID, w.Size())
				last = time.Now()
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// firstChunk reads what the first Read of body yields, up to 512 bytes, and
// returns it with a reader for the rest that reports body's error, if the
// first read ended in one.
func firstChunk(body io.Reader) (head []byte, rest io.Reader) {
	buf := make([]byte, 512)
	for i := 0; i < 100; i++ {
		n, err := body.Read(buf)
		if n > 0 || err != nil {
			switch {
			case err == nil:
				return buf[:n], body
			case errors.Is(err, io.EOF):
				return buf[:n], eof{}
			default:
				return buf[:n], failed{err}
			}
		}
	}
	return nil, body
}

type eof struct{}

func (eof) Read([]byte) (int, error) { return 0, io.EOF }

type failed struct{ err error }

func (f failed) Read([]byte) (int, error) { return 0, f.err }

// byExtension is the types cafard knows by name. A fixed table rather than
// the mime package's, which also reads the system's tables, so a file's type
// would depend on the host: Debian's knows extensions Alpine's does not.
var byExtension = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".heic": "image/heic", ".heif": "image/heif", ".avif": "image/avif",
	".svg": "image/svg+xml", ".pdf": "application/pdf", ".json": "application/json",
	".mp4": "video/mp4", ".mov": "video/quicktime", ".zip": "application/zip", ".gz": "application/gzip",
	".tar": "application/x-tar", ".bin": "application/octet-stream", ".img": "application/octet-stream",
	".html": "text/html; charset=utf-8", ".htm": "text/html; charset=utf-8", ".xml": "text/xml; charset=utf-8",
}

var textExtensions = map[string]bool{
	".txt": true, ".log": true, ".md": true, ".csv": true, ".yaml": true, ".yml": true, ".conf": true,
	".cfg": true, ".ini": true, ".sh": true, ".py": true, ".go": true, ".c": true, ".h": true, ".js": true,
	".ts": true, ".dmesg": true, ".out": true, ".diff": true, ".patch": true, ".toml": true,
}

// detectType decides a file's type from its name's extension first, then its
// first bytes, and only then what the sender declared: curl -T declares
// nothing, and a browser's declaration is often application/octet-stream.
// Bytes that are valid UTF-8 and nothing more specific are text, which is
// what makes a .log readable by line.
func detectType(name, declared string, head []byte) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := byExtension[ext]; ok {
		return t
	}
	if textExtensions[ext] {
		return "text/plain; charset=utf-8"
	}
	if len(head) > 0 {
		if t := http.DetectContentType(head); t != "application/octet-stream" {
			return t
		}
	}
	if declared != "" && !strings.HasPrefix(declared, "application/x-www-form-urlencoded") {
		return declared
	}
	if len(head) > 0 && utf8.Valid(head) {
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// Projects lists the projects who can read, by name.
func (s *Service) Projects(ctx context.Context, who access.Who) ([]store.Project, error) {
	ps, err := s.st.ProjectsOf(ctx, who.ID)
	if err != nil {
		return nil, err
	}
	var out []store.Project
	for _, p := range ps {
		if err := s.acc.CanRead(ctx, who, p); err != nil {
			if access.Is(err, access.SendOnly) || access.Is(err, access.Paused) || access.Is(err, access.Removed) {
				return nil, err
			}
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// readable is a project who may read, by name.
func (s *Service) readable(ctx context.Context, who access.Who, project string) (store.Project, error) {
	p, err := s.project(ctx, project)
	if err != nil {
		return p, err
	}
	return p, s.acc.CanRead(ctx, who, p)
}

// Conversations lists a project's conversations, most recently active first.
func (s *Service) Conversations(ctx context.Context, who access.Who, project string) ([]store.Conversation, error) {
	p, err := s.readable(ctx, who, project)
	if err != nil {
		return nil, err
	}
	return s.st.Conversations(ctx, p.ID)
}

// MaxPage is the most messages one read returns.
const MaxPage = 200

// Messages returns messages of a conversation after the one with id after,
// oldest first.
func (s *Service) Messages(ctx context.Context, who access.Who, project, conversation string, after int64, limit int) (store.Conversation, []store.Message, error) {
	return s.MessagesPage(ctx, who, project, conversation, Page{After: after, Limit: limit})
}

// Page is which messages to read: after a message (new ones, for a page
// that is open), before one (scrolling back), or else the latest, which is
// what a conversation opens on.
type Page struct {
	After, Before int64
	Limit         int
}

// MessagesPage reads a page of a conversation, oldest first.
func (s *Service) MessagesPage(ctx context.Context, who access.Who, project, conversation string, pg Page) (store.Conversation, []store.Message, error) {
	p, err := s.readable(ctx, who, project)
	if err != nil {
		return store.Conversation{}, nil, err
	}
	c, err := s.st.ConversationByName(ctx, p.ID, conversation)
	if errors.Is(err, store.ErrNotFound) {
		return c, nil, &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No conversation %q in %s.", conversation, p.Name)}
	}
	if err != nil {
		return c, nil, err
	}
	limit := pg.Limit
	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}
	var ms []store.Message
	switch {
	case pg.After > 0:
		ms, err = s.st.Messages(ctx, c.ID, pg.After, limit)
	case pg.Before > 0:
		ms, err = s.st.MessagesBefore(ctx, c.ID, pg.Before, limit)
	default:
		ms, err = s.st.LatestMessages(ctx, c.ID, limit)
	}
	if err != nil {
		return c, nil, err
	}
	// Being shown messages is reading them: the reader's position moves to
	// the last one, which is what "Read by" and unread counts come from.
	if len(ms) > 0 {
		if err := s.st.MarkRead(ctx, who.ID, c.ID, ms[len(ms)-1].ID); err != nil {
			return c, nil, err
		}
	}
	return c, ms, nil
}

// File is a file who may read, with a reader over its bytes: the whole blob,
// or what has arrived of a live one.
func (s *Service) File(ctx context.Context, who access.Who, id int64) (store.FileWhere, io.ReadSeekCloser, error) {
	fw, err := s.st.File(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return fw, nil, &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No file %d.", id)}
	}
	if err != nil {
		return fw, nil, err
	}
	p, err := s.st.Project(ctx, fw.ProjectID)
	if err != nil {
		return fw, nil, err
	}
	// Not found rather than not allowed, so a file id says nothing about a
	// project the reader is not in.
	if err := s.acc.CanRead(ctx, who, p); err != nil {
		if access.Is(err, access.NotMember) {
			return fw, nil, &access.Refusal{Code: access.NotFound, Message: fmt.Sprintf("No file %d.", id)}
		}
		return fw, nil, err
	}
	switch fw.State {
	case store.Stored:
		r, err := s.bl.Open(fw.SHA256)
		return fw, r, err
	case store.Live:
		r, err := s.bl.OpenStaged(strconv.FormatInt(fw.ID, 10))
		if errors.Is(err, blobs.ErrNotFound) {
			// It ended between reading its row and opening it: read it
			// again, now stored.
			if again, aerr := s.st.File(ctx, id); aerr == nil && again.State == store.Stored {
				r, err := s.bl.Open(again.SHA256)
				return again, r, err
			}
		}
		return fw, r, err
	case store.Cut:
		return fw, nil, &access.Refusal{Code: Gone, Message: fmt.Sprintf("%s was cut off: the server stopped while it was arriving.", fw.Name)}
	default:
		return fw, nil, &access.Refusal{Code: Gone, Message: fmt.Sprintf("%s was deleted.", fw.Name)}
	}
}

// Gone is a file whose bytes are no longer kept.
const Gone access.Code = "gone"
