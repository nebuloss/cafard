package attention

import (
	"context"
	"time"

	"cafard/internal/live"
	"cafard/internal/service/access"
	"cafard/internal/store"
)

// MaxWait bounds a wait below what MCP clients allow a tool call: Claude
// Code's default, and others', is a minute; a wait cut off by the client is
// an error the model sees, and one that ends at 50 s is "nothing yet".
const MaxWait = 50 * time.Second

// settle is how long a burst of writes is let finish before the inbox is
// read again: a message and its files arrive as several notices.
const settle = 300 * time.Millisecond

// Wait blocks until something addressed to who arrives in p, or until
// timeout. It returns what arrived during the call, and what was already
// waiting beside it: an agent looping on wait is woken by what is new, not
// again and again by what it chose to leave.
//
// It reads nothing on a timer. It listens on the project's topic in
// internal/live, which every message published there marks, and reads the
// inbox again only when that topic changes.
func (s *Service) Wait(ctx context.Context, who access.Who, p store.Project, timeout time.Duration) (fresh, waiting []Entry, err error) {
	if timeout <= 0 || timeout > MaxWait {
		timeout = MaxWait
	}
	key := live.ProjectKey(p.ID)
	sub := s.hub.Subscribe(func(k string) bool { return k == key })
	defer s.hub.Unsubscribe(sub)

	// The baseline is read after subscribing, so a message that lands
	// between the two is either in it or announced.
	base, err := s.Inbox(ctx, who, p)
	if err != nil {
		return nil, nil, err
	}
	seen := latest(base)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			waiting, err := s.Inbox(context.WithoutCancel(ctx), who, p)
			return nil, waiting, err
		case _, open := <-sub.C:
			if !open {
				return nil, base, nil // the server is stopping
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(settle):
		}
		now, err := s.Inbox(context.WithoutCancel(ctx), who, p)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range now {
			if e.Latest > seen[e.Conversation.ID] {
				fresh = append(fresh, e)
			} else {
				waiting = append(waiting, e)
			}
		}
		if len(fresh) > 0 {
			return fresh, waiting, nil
		}
		waiting = nil
	}
}

func latest(es []Entry) map[int64]int64 {
	out := make(map[int64]int64, len(es))
	for _, e := range es {
		out[e.Conversation.ID] = e.Latest
	}
	return out
}
