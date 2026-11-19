package messaging

import (
	"context"
	"strings"
	"sync"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// Chat event kinds, the SSE event names of CONTRACTS section 4.
const (
	EventSeen     = "seen"
	EventAnswered = "answered"
	EventChat     = "chat"
)

// ChatEvent is something about a chat that is not an entry: an ack, a
// question answered, or a change of the chat itself (metadata, bindings,
// prefs, archive state). The web feed turns them into SSE events.
type ChatEvent struct {
	Kind   string
	Stream string // the chat's stream id: chan:x or dm:a~b
	Seq    int64  // seen: the acked message; answered: the question
	By     string // seen: who acked
	Answer *dto.Answer
	// Only, when set, is the one principal the event concerns (pin, mute
	// and follow are per principal).
	Only string
	// Gone is set on a chat event for a channel that no longer exists
	// (deleted, or renamed away).
	Gone bool
}

// chatEvents fans events out to watchers without ever blocking the
// caller: a watcher that does not keep up loses events, which only costs
// it a stale receipt or badge until the next one.
type chatEvents struct {
	mu   sync.Mutex
	subs map[chan ChatEvent]struct{}
}

// WatchChats returns chat events from now on, and a function that stops
// them.
func (s *Service) WatchChats(buffer int) (<-chan ChatEvent, func()) {
	ch := make(chan ChatEvent, max(buffer, 1))
	s.events.mu.Lock()
	if s.events.subs == nil {
		s.events.subs = map[chan ChatEvent]struct{}{}
	}
	s.events.subs[ch] = struct{}{}
	s.events.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.events.mu.Lock()
			delete(s.events.subs, ch)
			s.events.mu.Unlock()
		})
	}
}

func (s *Service) emit(ev ChatEvent) {
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	for ch := range s.events.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (s *Service) emitChat(stream, only string) {
	s.emit(ChatEvent{Kind: EventChat, Stream: stream, Only: only})
}

// noteReply emits "answered" when e is the first human reply to a
// question.
func (s *Service) noteReply(ctx context.Context, e store.Entry) {
	if e.ReplyTo == 0 || s.kindOf(ctx, e.Sender) != auth.KindHuman {
		return
	}
	q, err := s.st.Get(ctx, e.ReplyTo)
	if err != nil || !IsQuestion(q) {
		return
	}
	a, err := s.answer(ctx, q.Seq)
	if err != nil || a == nil || a.Seq != e.Seq {
		return
	}
	s.emit(ChatEvent{Kind: EventAnswered, Stream: q.Stream, Seq: q.Seq, Answer: a})
}

// ChatOfStream is the Chat DTO of a chat stream from viewer's side.
func (s *Service) ChatOfStream(ctx context.Context, viewer auth.Principal, stream string) (dto.Chat, error) {
	if name, ok := cutChan(stream); ok && strings.Contains(name, "/") {
		ch, err := s.Channel(ctx, viewer, name)
		if err != nil {
			return dto.Chat{}, err
		}
		return s.ChannelChat(ctx, viewer, ch)
	}
	a, b, ok := DMParties(stream)
	if !ok || (viewer.Name != a && viewer.Name != b) {
		return dto.Chat{}, dto.Errorf(dto.CodeNotFound, "There is no chat %s.", stream)
	}
	peer := a
	if peer == viewer.Name {
		peer = b
	}
	return s.DirectChat(ctx, viewer, peer)
}
