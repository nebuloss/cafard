package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"cafard/internal/live"
)

// Conversation is a thread in a project.
type Conversation struct {
	ID        int64
	ProjectID int64
	Name      string
	Title     string
	CreatedAt time.Time
	ClosedAt  *time.Time
	// Filled by Conversations, for listing: how many messages, when the last
	// one was sent, who sent it, the start of its text, how many files it
	// has, and whether a file in the conversation is still arriving.
	Messages   int
	LastAt     *time.Time
	LastAuthor string
	LastText   string
	LastFiles  int
	Live       bool
}

// Message is text, files, or both, from one member.
type Message struct {
	ID             int64
	ConversationID int64
	Author         Identity
	Text           string
	CreatedAt      time.Time
	Files          []File
}

// FileState is where a file's bytes are.
type FileState string

const (
	// Live: still arriving; the bytes so far are in the blob store's staging
	// area under the file's id.
	Live FileState = "live"
	// Stored: whole, in the blob store under SHA256.
	Stored FileState = "stored"
	// Cut: the upload stopped before it ended, and what arrived is gone (the
	// server stopped mid-upload, and staging is cleared when it starts).
	Cut FileState = "cut"
	// Deleted: removed by its sender or an admin; the message stays.
	Deleted FileState = "deleted"
)

// File is a file attached to a message.
type File struct {
	ID          int64
	MessageID   int64
	Name        string
	ContentType string
	Size        int64
	SHA256      string
	State       FileState
	CreatedAt   time.Time
}

// NewFile is a file to attach to a new message. Its bytes arrive afterwards,
// through the blob store; it starts Live.
type NewFile struct {
	Name        string
	ContentType string
}

// EnsureConversation returns the conversation called name in a project,
// creating it with title if it does not exist. created says which.
func (s *Store) EnsureConversation(ctx context.Context, projectID int64, name, title string, by int64) (c Conversation, created bool, err error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO conversations (project_id, name, title, created_by, created_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (project_id, name) DO NOTHING`,
		projectID, name, title, by, s.now().UnixMilli())
	if err != nil {
		return Conversation{}, false, fmt.Errorf("create conversation %s: %w", name, err)
	}
	n, _ := res.RowsAffected()
	c, err = s.ConversationByName(ctx, projectID, name)
	if err == nil && n > 0 {
		s.publish(live.ProjectKey(projectID))
	}
	return c, n > 0, err
}

const conversationCols = `id, project_id, name, title, created_at, closed_at`

func scanConversation(r scanner) (Conversation, error) {
	var c Conversation
	var created int64
	var closed sql.NullInt64
	err := r.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Title, &created, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	c.CreatedAt = time.UnixMilli(created)
	c.ClosedAt = fromMillis(closed)
	return c, err
}

func (s *Store) ConversationByName(ctx context.Context, projectID int64, name string) (Conversation, error) {
	return scanConversation(s.db.QueryRowContext(ctx, `SELECT `+conversationCols+` FROM conversations
		WHERE project_id = ? AND name = ?`, projectID, name))
}

func (s *Store) Conversation(ctx context.Context, id int64) (Conversation, error) {
	return scanConversation(s.db.QueryRowContext(ctx, `SELECT `+conversationCols+` FROM conversations WHERE id = ?`, id))
}

// Conversations lists a project's conversations, most recently active first.
func (s *Store) Conversations(ctx context.Context, projectID int64) ([]Conversation, error) {
	// The last message's author, text and files come from correlated
	// subqueries on messages_by_conversation: a project lists tens of
	// conversations, not thousands, and one query keeps the list one read.
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixed("c", conversationCols)+`, COUNT(m.id), MAX(m.created_at),
		(SELECT i.name FROM messages lm JOIN identities i ON i.id = lm.author_id WHERE lm.conversation_id = c.id ORDER BY lm.id DESC LIMIT 1),
		(SELECT substr(lm.text, 1, 160) FROM messages lm WHERE lm.conversation_id = c.id ORDER BY lm.id DESC LIMIT 1),
		(SELECT COUNT(*) FROM files f WHERE f.message_id = (SELECT MAX(lm.id) FROM messages lm WHERE lm.conversation_id = c.id)),
		EXISTS (SELECT 1 FROM files f JOIN messages fm ON fm.id = f.message_id WHERE fm.conversation_id = c.id AND f.state = 'live')
		FROM conversations c LEFT JOIN messages m ON m.conversation_id = c.id
		WHERE c.project_id = ? GROUP BY c.id ORDER BY COALESCE(MAX(m.created_at), c.created_at) DESC, c.id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		var created int64
		var closed, last sql.NullInt64
		var author, text sql.NullString
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Title, &created, &closed, &c.Messages, &last,
			&author, &text, &c.LastFiles, &c.Live); err != nil {
			return nil, err
		}
		c.CreatedAt = time.UnixMilli(created)
		c.ClosedAt = fromMillis(closed)
		c.LastAt = fromMillis(last)
		c.LastAuthor, c.LastText = author.String, text.String
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddMessage posts a message with its files, in one transaction, and returns
// it with the files' ids.
func (s *Store) AddMessage(ctx context.Context, conversationID, authorID int64, text string, files []NewFile) (Message, error) {
	now := s.now()
	m := Message{ConversationID: conversationID, Text: text, CreatedAt: now}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO messages (conversation_id, author_id, text, created_at) VALUES (?, ?, ?, ?)`,
		conversationID, authorID, text, now.UnixMilli())
	if err != nil {
		return Message{}, fmt.Errorf("post message: %w", err)
	}
	if m.ID, err = res.LastInsertId(); err != nil {
		return Message{}, err
	}
	for _, f := range files {
		res, err := tx.ExecContext(ctx, `INSERT INTO files (message_id, name, content_type, state, created_at) VALUES (?, ?, ?, 'live', ?)`,
			m.ID, f.Name, f.ContentType, now.UnixMilli())
		if err != nil {
			return Message{}, fmt.Errorf("attach %s: %w", f.Name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return Message{}, err
		}
		m.Files = append(m.Files, File{ID: id, MessageID: m.ID, Name: f.Name, ContentType: f.ContentType, State: Live, CreatedAt: now})
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	s.publishConversation(ctx, conversationID)
	if m.Author, err = s.Identity(ctx, authorID); err != nil {
		return Message{}, err
	}
	return m, nil
}

// StoreFile records that a live file arrived whole.
func (s *Store) StoreFile(ctx context.Context, id int64, sha256 string, size int64) error {
	err := one(s.db.ExecContext(ctx, `UPDATE files SET state = 'stored', sha256 = ?, size = ? WHERE id = ? AND state = 'live'`,
		sha256, size, id))
	if err == nil {
		s.publishFile(ctx, id)
	}
	return err
}

// GrowFile records how much of a live file has arrived, for readers, and
// tells them it grew.
func (s *Store) GrowFile(ctx context.Context, id int64, size int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE files SET size = ? WHERE id = ? AND state = 'live'`, size, id)
	if err == nil {
		s.publishFile(ctx, id)
	}
	return err
}

// CutLiveFiles marks every file still live as cut. Run when the server
// starts: an upload in progress when it stopped did not finish, and its
// staged bytes are gone.
func (s *Store) CutLiveFiles(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE files SET state = 'cut' WHERE state = 'live'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const fileCols = `id, message_id, name, content_type, size, sha256, state, created_at`

func scanFile(r scanner) (File, error) {
	var f File
	var sum sql.NullString
	var created int64
	err := r.Scan(&f.ID, &f.MessageID, &f.Name, &f.ContentType, &f.Size, &sum, &f.State, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	f.SHA256 = sum.String
	f.CreatedAt = time.UnixMilli(created)
	return f, err
}

// FileWhere is a file with the conversation and project it belongs to, which
// is what deciding who may read it needs, and who sent it and where, which
// is what an agent shown it needs to weigh it.
type FileWhere struct {
	File
	ConversationID   int64
	ProjectID        int64
	ConversationName string
	AuthorName       string
	AuthorKind       Kind
}

func (s *Store) File(ctx context.Context, id int64) (FileWhere, error) {
	var fw FileWhere
	var sum sql.NullString
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT `+prefixed("f", fileCols)+`, c.id, c.project_id, c.name, i.name, i.kind FROM files f
		JOIN messages m ON m.id = f.message_id JOIN conversations c ON c.id = m.conversation_id
		JOIN identities i ON i.id = m.author_id WHERE f.id = ?`, id).
		Scan(&fw.ID, &fw.MessageID, &fw.Name, &fw.ContentType, &fw.Size, &sum, &fw.State, &created, &fw.ConversationID, &fw.ProjectID,
			&fw.ConversationName, &fw.AuthorName, &fw.AuthorKind)
	if errors.Is(err, sql.ErrNoRows) {
		return FileWhere{}, ErrNotFound
	}
	if err != nil {
		return FileWhere{}, err
	}
	fw.SHA256 = sum.String
	fw.CreatedAt = time.UnixMilli(created)
	return fw, nil
}

// Messages returns up to limit messages of a conversation after the message
// with id after (0 for the start), oldest first, with their authors and files.
func (s *Store) Messages(ctx context.Context, conversationID, after int64, limit int) ([]Message, error) {
	return s.messages(ctx, `SELECT m.id, m.text, m.created_at, `+prefixed("i", identityCols)+`
		FROM messages m JOIN identities i ON i.id = m.author_id
		WHERE m.conversation_id = ? AND m.id > ? ORDER BY m.id LIMIT ?`, conversationID, conversationID, after, limit)
}

// LatestMessages returns the last limit messages of a conversation, oldest
// first: what a conversation opens on. Earlier ones come with Before.
func (s *Store) LatestMessages(ctx context.Context, conversationID int64, limit int) ([]Message, error) {
	return s.messages(ctx, `SELECT * FROM (SELECT m.id, m.text, m.created_at, `+prefixed("i", identityCols)+`
		FROM messages m JOIN identities i ON i.id = m.author_id
		WHERE m.conversation_id = ? ORDER BY m.id DESC LIMIT ?) ORDER BY 1`, conversationID, conversationID, limit)
}

// MessagesBefore returns up to limit messages before the message with id
// before, oldest first: scrolling back through a long conversation.
func (s *Store) MessagesBefore(ctx context.Context, conversationID, before int64, limit int) ([]Message, error) {
	return s.messages(ctx, `SELECT * FROM (SELECT m.id, m.text, m.created_at, `+prefixed("i", identityCols)+`
		FROM messages m JOIN identities i ON i.id = m.author_id
		WHERE m.conversation_id = ? AND m.id < ? ORDER BY m.id DESC LIMIT ?) ORDER BY 1`, conversationID, conversationID, before, limit)
}

func (s *Store) messages(ctx context.Context, query string, conversationID int64, args ...any) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []Message
	index := map[int64]int{}
	for rows.Next() {
		var m Message
		var created, iCreated int64
		var deleted sql.NullInt64
		a := &m.Author
		if err := rows.Scan(&m.ID, &m.Text, &created, &a.ID, &a.Name, &a.Kind, &a.DisplayName, &a.Description,
			&a.Admin, &a.Enabled, &iCreated, &deleted); err != nil {
			rows.Close()
			return nil, err
		}
		m.ConversationID = conversationID
		m.CreatedAt = time.UnixMilli(created)
		a.CreatedAt = time.UnixMilli(iCreated)
		a.DeletedAt = fromMillis(deleted)
		index[m.ID] = len(out)
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}

	ids := make([]string, 0, len(out))
	fileArgs := make([]any, 0, len(out))
	for _, m := range out {
		ids = append(ids, "?")
		fileArgs = append(fileArgs, m.ID)
	}
	frows, err := s.db.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE message_id IN (`+strings.Join(ids, ", ")+`) ORDER BY id`, fileArgs...)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		f, err := scanFile(frows)
		if err != nil {
			return nil, err
		}
		m := &out[index[f.MessageID]]
		m.Files = append(m.Files, f)
	}
	return out, frows.Err()
}
