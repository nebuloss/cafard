package messaging

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// FollowState is a principal's relation to a chat.
type FollowState struct {
	Followed, Pinned, Muted bool
	Explicit                bool // a row exists
}

// Follow returns p's follow state of chat (a stream id). Humans follow
// every channel unless they unfollowed it; agents only the ones they
// followed. Both parties follow a direct conversation.
func (s *Service) Follow(ctx context.Context, p auth.Principal, chat string) (FollowState, error) {
	var st FollowState
	var followed, pinned, muted int
	err := s.db.QueryRowContext(ctx, `SELECT followed, pinned, muted FROM follows WHERE principal = ? AND chat = ?`, p.Name, chat).
		Scan(&followed, &pinned, &muted)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		st.Followed = p.Kind == auth.KindHuman || strings.HasPrefix(chat, "dm:")
	case err != nil:
		return st, err
	default:
		st = FollowState{Followed: followed == 1, Pinned: pinned == 1, Muted: muted == 1, Explicit: true}
	}
	return st, nil
}

// SetFollow follows or unfollows a channel for p.
func (s *Service) SetFollow(ctx context.Context, p auth.Principal, channel string, on bool) (FollowState, error) {
	if err := needMessage(p); err != nil {
		return FollowState{}, err
	}
	ch, err := s.Channel(ctx, channel)
	if err != nil {
		return FollowState{}, err
	}
	if err := s.setFollow(ctx, p.Name, ch.Stream(), on); err != nil {
		return FollowState{}, err
	}
	return s.Follow(ctx, p, ch.Stream())
}

func (s *Service) setFollow(ctx context.Context, principal, chat string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO follows(principal, chat, followed) VALUES (?, ?, ?)
		ON CONFLICT(principal, chat) DO UPDATE SET followed = excluded.followed`, principal, chat, v)
	return err
}

// SetPrefs changes pin and mute (the web UI's chat prefs).
func (s *Service) SetPrefs(ctx context.Context, p auth.Principal, chat string, pinned, muted *bool) error {
	cur, err := s.Follow(ctx, p, chat)
	if err != nil {
		return err
	}
	if pinned != nil {
		cur.Pinned = *pinned
	}
	if muted != nil {
		cur.Muted = *muted
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO follows(principal, chat, followed, pinned, muted) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(principal, chat) DO UPDATE SET pinned = excluded.pinned, muted = excluded.muted`,
		p.Name, chat, b2i(cur.Followed), b2i(cur.Pinned), b2i(cur.Muted))
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// autoFollow makes an agent follow a chat it used, unless it chose
// otherwise before (a row exists).
func (s *Service) autoFollow(ctx context.Context, principal, chat string) error {
	if !s.principalExists(ctx, principal) {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO follows(principal, chat, followed) VALUES (?, ?, 1) ON CONFLICT DO NOTHING`, principal, chat)
	return err
}

// ChannelChat is the Chat DTO of a channel, from p's side.
func (s *Service) ChannelChat(ctx context.Context, p auth.Principal, ch Channel) (dto.Chat, error) {
	fs, err := s.Follow(ctx, p, ch.Stream())
	if err != nil {
		return dto.Chat{}, err
	}
	ids, err := s.BoundStreams(ctx, ch)
	if err != nil {
		return dto.Chat{}, err
	}
	status := map[string]dto.StreamState{}
	if s.status != nil {
		for _, st := range s.status.Snapshot() {
			status[st.ID] = st.Status
		}
	}
	refs := make([]dto.StreamRef, 0, len(ids))
	for _, id := range ids {
		st := status[id]
		if st == "" {
			st = dto.StreamIdle
		}
		refs = append(refs, dto.StreamRef{ID: id, Status: st})
	}
	c := dto.Chat{
		ID: ch.Stream(), Kind: dto.ChatChannel, Name: ch.Name, Title: ch.Title, Topic: ch.Topic,
		Patterns: ch.Patterns, Streams: refs, Surface: ch.Surface,
		Followed: fs.Followed, Pinned: fs.Pinned, Muted: fs.Muted, Archived: ch.Archived(),
		Source: ch.Source, CreatedBy: ch.CreatedBy, CanManage: ch.CanManage(p),
		LastActivity: dto.TimePtr(ch.LastActivity),
	}
	if ch.CreatedBy == systemName {
		c.CreatedBy = ""
	}
	if c.Unread, err = s.unread(ctx, p, ch.Stream()); err != nil {
		return dto.Chat{}, err
	}
	if c.OpenQuestions, err = s.openQuestions(ctx, ch.Stream()); err != nil {
		return dto.Chat{}, err
	}
	if c.Preview, err = s.preview(ctx, ch.Stream()); err != nil {
		return dto.Chat{}, err
	}
	return c, nil
}

// DirectChat is the Chat DTO of p's conversation with peer.
func (s *Service) DirectChat(ctx context.Context, p auth.Principal, peer string) (dto.Chat, error) {
	stream := DMStream(p.Name, peer)
	fs, err := s.Follow(ctx, p, stream)
	if err != nil {
		return dto.Chat{}, err
	}
	c := dto.Chat{ID: "dm:" + peer, Kind: dto.ChatDirect, Name: peer, Surface: dto.SurfaceNone,
		Followed: fs.Followed, Pinned: fs.Pinned, Muted: fs.Muted, Source: dto.SourceRuntime}
	if row, err := s.tokens.Principal(ctx, peer); err == nil {
		c.Peer = &dto.Principal{Name: row.Name, Kind: dto.PrincipalKind(row.Kind)}
		if row.LastSeen != nil {
			c.Peer.LastSeen = dto.TimePtr(*row.LastSeen)
		}
	}
	if c.Unread, err = s.unread(ctx, p, stream); err != nil {
		return dto.Chat{}, err
	}
	if c.OpenQuestions, err = s.openQuestions(ctx, stream); err != nil {
		return dto.Chat{}, err
	}
	if c.Preview, err = s.preview(ctx, stream); err != nil {
		return dto.Chat{}, err
	}
	if c.Preview != nil {
		if e, err := s.st.Get(ctx, c.Preview.Seq); err == nil {
			c.LastActivity = dto.TimePtr(e.TS)
		}
	}
	return c, nil
}

// unread counts the messages of a chat p has not acked, its own excluded.
func (s *Service) unread(ctx context.Context, p auth.Principal, stream string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM entries e WHERE e.stream = ? AND e.kind = 'message'
		AND coalesce(e.sender, '') != ? AND NOT EXISTS (SELECT 1 FROM acks a WHERE a.principal = ? AND a.seq = e.seq)`,
		stream, p.Name, p.Name).Scan(&n)
	return n, err
}

// openQuestions counts questions of a chat no human has answered.
func (s *Service) openQuestions(ctx context.Context, stream string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM entries q WHERE q.stream = ? AND q.kind = 'message'
		AND q.meta IS NOT NULL AND json_type(q.meta, '$.choices') IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM entries r JOIN principals pr ON pr.name = r.sender
			WHERE r.reply_to = q.seq AND r.kind = 'message' AND pr.kind = 'human')`, stream).Scan(&n)
	return n, err
}

func (s *Service) preview(ctx context.Context, stream string) (*dto.ChatPreview, error) {
	var (
		seq          int64
		sender, text sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `SELECT seq, sender, text FROM entries WHERE stream = ? AND kind = 'message' ORDER BY seq DESC LIMIT 1`, stream).
		Scan(&seq, &sender, &text)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &dto.ChatPreview{Seq: seq, Sender: sender.String, Text: previewText(text.String, 140)}, nil
}

// previewText is the first line of text, at most n runes.
func previewText(text string, n int) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i] + " ..."
	}
	r := []rune(text)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return text
}
