package messaging

import (
	"context"
	"slices"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// TimelineQuery pages a chat's timeline: forward from SinceSeq, or
// backward before BeforeSeq.
type TimelineQuery struct {
	SinceSeq  int64
	BeforeSeq int64
	Limit     int
}

// TimelineRow is an entry of a timeline; Event marks a row from a bound
// stream.
type TimelineRow struct {
	store.Entry
	Event bool
}

// TimelinePage is one page of a timeline, in ascending seq order.
type TimelinePage struct {
	Rows    []TimelineRow
	HasMore bool
	NextSeq int64 // continue forward from here
}

var errorSeverity = 3

// Timeline is a chat as its page shows it: the chat's own entries and,
// per the channel's surface, markers and error lines of its bound
// streams. The union is computed here, on read, so a binding change
// rewrites the history too.
func (s *Service) Timeline(ctx context.Context, viewer auth.Principal, stream string, q TimelineQuery) (TimelinePage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	if !s.Visible(ctx, viewer, store.Entry{Stream: stream}) {
		return TimelinePage{}, dto.Errorf(dto.CodeNotFound, "There is no chat %s.", ChatID(viewer.Name, stream))
	}
	queries := []store.Query{{Streams: []string{stream}}}
	if name, ok := cutChan(stream); ok {
		ch, err := s.Channel(ctx, name)
		if err != nil {
			return TimelinePage{}, err
		}
		if ch.Surface != dto.SurfaceNone && len(ch.Patterns) > 0 {
			bound, err := s.BoundStreams(ctx, ch)
			if err != nil {
				return TimelinePage{}, err
			}
			if len(bound) > 0 {
				queries = append(queries, store.Query{Streams: bound, Kinds: []store.Kind{store.KindMarker}})
				if ch.Surface == dto.SurfaceErrors {
					queries = append(queries, store.Query{Streams: bound, Kinds: []store.Kind{store.KindLine}, MinSeverity: &errorSeverity})
				}
			}
		}
	}
	var (
		rows []TimelineRow
		more bool
	)
	for i, sq := range queries {
		sq.Limit = limit
		sq.SinceSeq = q.SinceSeq
		if q.BeforeSeq > 0 {
			sq.BeforeSeq, sq.Desc = q.BeforeSeq, true
		}
		res, err := s.st.Read(ctx, sq)
		if err != nil {
			return TimelinePage{}, err
		}
		if res.HasMore || (sq.Desc && len(res.Entries) == limit) {
			more = true
		}
		for _, e := range res.Entries {
			rows = append(rows, TimelineRow{Entry: e, Event: i > 0})
		}
	}
	slices.SortFunc(rows, func(a, b TimelineRow) int { return cmpInt(a.Seq, b.Seq) })
	if len(rows) > limit {
		more = true
		if q.BeforeSeq > 0 {
			rows = rows[len(rows)-limit:]
		} else {
			rows = rows[:limit]
		}
	}
	page := TimelinePage{Rows: rows, HasMore: more, NextSeq: q.SinceSeq}
	if len(rows) > 0 {
		page.NextSeq = rows[len(rows)-1].Seq + 1
	}
	if q.BeforeSeq == 0 && !more {
		if last, err := s.st.LastSeq(ctx); err == nil {
			page.NextSeq = max(page.NextSeq, last+1)
		}
	}
	return page, nil
}

func cutChan(stream string) (string, bool) {
	if len(stream) > 5 && stream[:5] == "chan:" {
		return stream[5:], true
	}
	return "", false
}

// TimelineDTO converts a page for viewer.
func (s *Service) TimelineDTO(ctx context.Context, viewer auth.Principal, page TimelinePage) (dto.Page[dto.Entry], error) {
	out := dto.Page[dto.Entry]{HasMore: page.HasMore, NextSeq: page.NextSeq}
	for _, r := range page.Rows {
		e, err := s.EntryDTO(ctx, viewer, r.Entry)
		if err != nil {
			return out, err
		}
		e.Event = r.Event
		out.Items = append(out.Items, e)
	}
	if len(page.Rows) > 0 {
		out.NextBeforeSeq = page.Rows[0].Seq
	}
	return out, nil
}
