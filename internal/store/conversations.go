package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"cafard/internal/live"
)

// CreateConversation adds a conversation called name, or ErrExists: unlike
// EnsureConversation, it never returns one that was there, so the chat app
// starting a conversation from its first message cannot land in another's.
func (s *Store) CreateConversation(ctx context.Context, projectID int64, name, title string, by int64) (Conversation, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversations (project_id, name, title, created_by, created_at) VALUES (?, ?, ?, ?, ?)`,
		projectID, name, title, by, s.now().UnixMilli())
	if isUnique(err) {
		return Conversation{}, ErrExists
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("create conversation %s: %w", name, err)
	}
	c, err := s.ConversationByName(ctx, projectID, name)
	if err == nil {
		s.publish(live.ProjectKey(projectID))
	}
	return c, err
}

// SetConversationTitle renames a conversation.
func (s *Store) SetConversationTitle(ctx context.Context, id int64, title string) error {
	err := one(s.db.ExecContext(ctx, `UPDATE conversations SET title = ? WHERE id = ?`, title, id))
	if err == nil {
		s.publishConversation(ctx, id)
	}
	return err
}

// SetConversationClosed closes a conversation, or opens it again.
func (s *Store) SetConversationClosed(ctx context.Context, id int64, closed bool) error {
	var at any
	if closed {
		at = s.now().UnixMilli()
	}
	err := one(s.db.ExecContext(ctx, `UPDATE conversations SET closed_at = ? WHERE id = ?`, at, id))
	if err == nil {
		s.publishConversation(ctx, id)
	}
	return err
}

// MessageInfo is what deciding about a message needs: where it is, who
// wrote it, and when.
type MessageInfo struct {
	ID             int64
	ConversationID int64
	AuthorID       int64
	CreatedAt      time.Time
}

func (s *Store) MessageInfo(ctx context.Context, id int64) (MessageInfo, error) {
	var m MessageInfo
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, conversation_id, author_id, created_at FROM messages WHERE id = ?`, id).
		Scan(&m.ID, &m.ConversationID, &m.AuthorID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageInfo{}, ErrNotFound
	}
	m.CreatedAt = time.UnixMilli(created)
	return m, err
}

// AddFile attaches a file to a message that exists: how a message with a
// caption and several photos is sent, one upload after another.
func (s *Store) AddFile(ctx context.Context, messageID int64, f NewFile) (File, error) {
	now := s.now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO files (message_id, name, content_type, state, created_at) VALUES (?, ?, ?, 'live', ?)`,
		messageID, f.Name, f.ContentType, now.UnixMilli())
	if err != nil {
		return File{}, fmt.Errorf("attach %s: %w", f.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return File{}, err
	}
	s.publishFile(ctx, id)
	return File{ID: id, MessageID: messageID, Name: f.Name, ContentType: f.ContentType, State: Live, CreatedAt: now}, nil
}

// DeleteFile marks a stored file deleted and says whether its bytes are
// still attached elsewhere: the blob is removed only when they are not.
func (s *Store) DeleteFile(ctx context.Context, id int64, a AuditEntry) (sha256 string, stillUsed bool, err error) {
	err = s.change(ctx, a, func(tx *sql.Tx) error {
		var sum sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT sha256 FROM files WHERE id = ? AND state = 'stored'`, id).Scan(&sum); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		sha256 = sum.String
		if _, err := tx.ExecContext(ctx, `UPDATE files SET state = 'deleted' WHERE id = ?`, id); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE sha256 = ? AND state = 'stored'`, sha256).Scan(&n); err != nil {
			return err
		}
		stillUsed = n > 0
		return nil
	})
	if err == nil {
		s.publishFile(ctx, id)
	}
	return sha256, stillUsed, err
}

// FileAuthor is who sent a file.
func (s *Store) FileAuthor(ctx context.Context, fileID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT m.author_id FROM files f JOIN messages m ON m.id = f.message_id WHERE f.id = ?`, fileID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// MarkRead records that a member was shown a conversation up to a message.
// A read position only moves forward.
func (s *Store) MarkRead(ctx context.Context, identityID, conversationID, messageID int64) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO reads (identity_id, conversation_id, message_id, at) VALUES (?, ?, ?, ?)
		ON CONFLICT (identity_id, conversation_id) DO UPDATE SET message_id = excluded.message_id, at = excluded.at
		WHERE excluded.message_id > reads.message_id`, identityID, conversationID, messageID, s.now().UnixMilli())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.publishConversation(ctx, conversationID)
	}
	return nil
}

// Read is how far one member has read a conversation.
type Read struct {
	Member    Identity
	MessageID int64
	At        time.Time
}

// Reads lists who has read a conversation, and how far.
func (s *Store) Reads(ctx context.Context, conversationID int64) ([]Read, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.message_id, r.at, `+prefixed("i", identityCols)+` FROM reads r
		JOIN identities i ON i.id = r.identity_id WHERE r.conversation_id = ? ORDER BY r.at`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Read
	for rows.Next() {
		var r Read
		var at, created int64
		var deleted sql.NullInt64
		i := &r.Member
		if err := rows.Scan(&r.MessageID, &at, &i.ID, &i.Name, &i.Kind, &i.DisplayName, &i.Description, &i.Admin, &i.Enabled, &created, &deleted); err != nil {
			return nil, err
		}
		r.At, i.CreatedAt, i.DeletedAt = time.UnixMilli(at), time.UnixMilli(created), fromMillis(deleted)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Unread counts, per conversation of a project, the messages a member has
// not been shown, leaving out their own.
func (s *Store) Unread(ctx context.Context, identityID, projectID int64) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.conversation_id, COUNT(*) FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		LEFT JOIN reads r ON r.conversation_id = m.conversation_id AND r.identity_id = ?
		WHERE c.project_id = ? AND m.author_id <> ? AND m.id > COALESCE(r.message_id, 0)
		GROUP BY m.conversation_id`, identityID, projectID, identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var c int64
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

// ConversationsByID is a project's conversations keyed by id, for the
// readers that need names from ids.
func (s *Store) ConversationsByID(ctx context.Context, projectID int64) (map[int64]Conversation, error) {
	cs, err := s.Conversations(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Conversation, len(cs))
	for _, c := range cs {
		out[c.ID] = c
	}
	return out, nil
}

// Unseen is a message a member has not been shown, as service/attention
// needs it to decide whether it is addressed to them: who wrote it, its
// text, and what it carries.
type Unseen struct {
	ID             int64
	ConversationID int64
	AuthorName     string
	AuthorDisplay  string
	AuthorKind     Kind
	Text           string
	Files, Images  int
	CreatedAt      time.Time
}

// Unseen lists, oldest first, the messages in a project's open conversations
// that a member has not been shown, leaving out their own: at most limit,
// the newest ones if there are more.
func (s *Store) Unseen(ctx context.Context, identityID, projectID int64, limit int) ([]Unseen, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM (SELECT m.id, m.conversation_id, i.name, i.display_name, i.kind, m.text, m.created_at,
		(SELECT COUNT(*) FROM files f WHERE f.message_id = m.id AND f.state <> 'deleted'),
		(SELECT COUNT(*) FROM files f WHERE f.message_id = m.id AND f.state <> 'deleted' AND f.content_type LIKE 'image/%')
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		JOIN identities i ON i.id = m.author_id
		LEFT JOIN reads r ON r.conversation_id = m.conversation_id AND r.identity_id = ?
		WHERE c.project_id = ? AND c.closed_at IS NULL AND m.author_id <> ? AND m.id > COALESCE(r.message_id, 0)
		ORDER BY m.id DESC LIMIT ?) ORDER BY 1`, identityID, projectID, identityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unseen
	for rows.Next() {
		var u Unseen
		var at int64
		if err := rows.Scan(&u.ID, &u.ConversationID, &u.AuthorName, &u.AuthorDisplay, &u.AuthorKind, &u.Text, &at, &u.Files, &u.Images); err != nil {
			return nil, err
		}
		u.CreatedAt = time.UnixMilli(at)
		out = append(out, u)
	}
	return out, rows.Err()
}

// PostedIn is the set of a project's conversations a member has posted in.
func (s *Store) PostedIn(ctx context.Context, identityID, projectID int64) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT m.conversation_id FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE c.project_id = ? AND m.author_id = ?`, projectID, identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
