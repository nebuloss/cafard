// Package messaging is messages between agents and humans: sending,
// visibility, inboxes, acks, follows, questions, channels bound to
// streams, claims and log attachments.
package messaging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/blob"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/hub"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/writer"
)

// Options are the config sections messaging reads.
type Options struct {
	Config   Config
	Channels Channels
	Images   blob.Config
	// BlobsMaxBytes is retention.blobs_max_bytes; 0 means no limit.
	BlobsMaxBytes int64
}

// Execer runs a function inside the writer, in its own transaction. Only
// the writer touches entries, so renaming and deleting a channel go
// through it.
type Execer interface {
	Exec(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error
}

// StagedLookup returns the files of a staged share (track D's /share).
type StagedLookup func(ctx context.Context, p auth.Principal, id string) ([]dto.Blob, error)

// Service implements deps.Messaging.
type Service struct {
	st     *store.Store
	db     *sql.DB
	w      writer.Writer
	exec   Execer
	hub    hub.Hub
	status statusSnapshot
	tokens *auth.Tokens
	blobs  *blob.Store
	clock  clock.Clock
	log    *slog.Logger
	opts   Options

	sendLimit   *limiter
	createLimit *limiter

	kinds sync.Map // principal name -> kind; a kind never changes

	mu     sync.Mutex
	staged StagedLookup
	onAck  []func(seq int64, by string)
	mcp    *mcpserver.Server
	subsMu sync.Mutex
	subs   map[string]map[string]*subscription // uri -> principal
}

type statusSnapshot interface {
	Snapshot() []dto.Stream
}

var _ deps.Messaging = (*Service)(nil)

// New builds the service and its blob store, makes sure #general exists
// and reapplies the config channels. It writes no entries: the writer is
// not running yet when New is called.
func New(ctx context.Context, d deps.Deps, o Options) (*Service, error) {
	blobs, err := blob.New(d.Store, d.Server.DataDir, o.Images, d.Clock, d.Log.With("component", "blobs"))
	if err != nil {
		return nil, err
	}
	s := &Service{
		st: d.Store, db: d.Store.DB(), w: d.Writer, hub: d.Hub, tokens: d.Tokens, blobs: blobs,
		clock: d.Clock, log: d.Log.With("component", "messaging"), opts: o,
		sendLimit:   newLimiter(d.Clock, float64(o.Config.RatePerMinute)/60, float64(o.Config.Burst)),
		createLimit: newLimiter(d.Clock, float64(o.Config.ChannelCreatePerHour)/3600, float64(o.Config.ChannelCreatePerHour)),
		subs:        map[string]map[string]*subscription{},
	}
	if e, ok := d.Writer.(Execer); ok {
		s.exec = e
	}
	if d.Status != nil {
		s.status = d.Status
	}
	blobs.SetVisible(s.Visible)
	if m, ok := d.Hub.(interface{ SetVisible(hub.VisibleFunc) }); ok {
		m.SetVisible(func(p auth.Principal, e store.Entry) bool { return s.Visible(context.Background(), p, e) })
	}
	if err := s.applyConfigChannels(ctx); err != nil {
		return nil, fmt.Errorf("channels: %w", err)
	}
	return s, nil
}

// Blobs is the blob store, for deps.Deps.Blobs.
func (s *Service) Blobs() *blob.Store { return s.blobs }

// SetStagedLookup lets the share target resolve `staged` ids.
func (s *Service) SetStagedLookup(f StagedLookup) {
	s.mu.Lock()
	s.staged = f
	s.mu.Unlock()
}

// OnAck registers a function told about every new ack (SSE "seen").
func (s *Service) OnAck(f func(seq int64, by string)) {
	s.mu.Lock()
	s.onAck = append(s.onAck, f)
	s.mu.Unlock()
}

// Janitor tasks: acks of deleted entries, blobs, expired claims, follows
// of deleted chats.
func (s *Service) JanitorTasks() []store.Task {
	tasks := []store.Task{
		{Name: "acks of deleted entries", Run: s.deleteOrphanAcks},
		{Name: "expired claims", Run: func(ctx context.Context) error { _, err := s.ExpireClaims(ctx); return err }},
		{Name: "follows of deleted chats", Run: s.deleteOrphanFollows},
	}
	return append(tasks, s.blobs.Tasks(s.opts.BlobsMaxBytes)...)
}

// Start runs the auto-archive loop and the resource notifications until
// ctx ends.
func (s *Service) Start(ctx context.Context) error {
	go s.watchResources(ctx)
	for {
		if _, err := s.ArchiveIdle(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("auto-archive failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.clock.After(time.Hour):
		}
	}
}

func (s *Service) deleteOrphanAcks(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM acks WHERE NOT EXISTS (SELECT 1 FROM entries e WHERE e.seq = acks.seq)`)
	return err
}

func (s *Service) deleteOrphanFollows(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM follows WHERE chat LIKE 'chan:%'
		AND NOT EXISTS (SELECT 1 FROM channels c WHERE 'chan:' || c.name = follows.chat)`); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT chat FROM follows WHERE chat LIKE 'dm:%'`)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var chat string
		if err := rows.Scan(&chat); err != nil {
			rows.Close()
			return err
		}
		a, b, ok := DMParties(chat)
		if !ok || !s.principalExists(ctx, a) || !s.principalExists(ctx, b) {
			gone = append(gone, chat)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, chat := range gone {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM follows WHERE chat = ?`, chat); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) principalExists(ctx context.Context, name string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM principals WHERE name = ?`, name).Scan(&n)
	return n > 0
}

// kindOf is a principal's kind, "" when unknown.
func (s *Service) kindOf(ctx context.Context, name string) string {
	if name == "" {
		return ""
	}
	if k, ok := s.kinds.Load(name); ok {
		return k.(string)
	}
	var kind string
	if err := s.db.QueryRowContext(ctx, `SELECT kind FROM principals WHERE name = ?`, name).Scan(&kind); err != nil {
		return ""
	}
	s.kinds.Store(name, kind)
	return kind
}

// DMStream is the stream of the direct conversation between a and b.
func DMStream(a, b string) string {
	if b < a {
		a, b = b, a
	}
	return "dm:" + a + "~" + b
}

// DMParties splits a dm: stream id into its two names.
func DMParties(stream string) (a, b string, ok bool) {
	rest, found := strings.CutPrefix(stream, "dm:")
	if !found {
		return "", "", false
	}
	a, b, ok = strings.Cut(rest, "~")
	return a, b, ok && a != "" && b != ""
}

// Visible reports whether p may see e. Log entries are visible to
// everyone; a channel to every reader; a direct conversation to its two
// parties only, admins included.
func (s *Service) Visible(_ context.Context, p auth.Principal, e store.Entry) bool {
	if !store.IsMessageStream(e.Stream) {
		return true
	}
	if a, b, ok := DMParties(e.Stream); ok {
		return p.Name != "" && (p.Name == a || p.Name == b)
	}
	if strings.HasPrefix(e.Stream, "dm:") {
		return false
	}
	return p.Has(auth.ScopeRead)
}

// ChatOf is the chat a message stream entry belongs to.
func (s *Service) ChatOf(e store.Entry) string {
	if store.IsMessageStream(e.Stream) {
		return e.Stream
	}
	return ""
}

// ChatID is a chat id from the viewer's side: "chan:x", or "dm:<peer>".
func ChatID(viewer, stream string) string {
	if a, b, ok := DMParties(stream); ok {
		switch viewer {
		case a:
			return "dm:" + b
		case b:
			return "dm:" + a
		}
	}
	return stream
}

// StreamOfChat turns a viewer-side chat id into a stream id.
func StreamOfChat(viewer, chat string) (string, error) {
	switch {
	case strings.HasPrefix(chat, "chan:"):
		return chat, nil
	case strings.HasPrefix(chat, "dm:"):
		peer := strings.TrimPrefix(chat, "dm:")
		if strings.Contains(peer, "~") {
			if a, b, ok := DMParties(chat); ok && (a == viewer || b == viewer) {
				return chat, nil
			}
			return "", dto.Errorf(dto.CodeNotFound, "There is no chat %s.", chat)
		}
		if peer == "" || peer == viewer {
			return "", dto.Errorf(dto.CodeBadRequest, "A direct chat needs another principal: dm:<name>.")
		}
		return DMStream(viewer, peer), nil
	}
	return "", dto.Errorf(dto.CodeBadRequest, "A chat id is chan:<name> or dm:<principal>, not %q.", chat)
}

func notFoundMessage(seq int64) *dto.APIError {
	return dto.Errorf(dto.CodeNotFound, "Message %d does not exist.", seq)
}

// Message returns a message (or a chat marker) visible to p.
func (s *Service) Message(ctx context.Context, p auth.Principal, seq int64) (store.Entry, error) {
	e, err := s.st.Get(ctx, seq)
	if errors.Is(err, store.ErrNotFound) {
		return store.Entry{}, notFoundMessage(seq)
	}
	if err != nil {
		return store.Entry{}, err
	}
	if !store.IsMessageStream(e.Stream) || !s.Visible(ctx, p, e) {
		return store.Entry{}, notFoundMessage(seq)
	}
	return e, nil
}
