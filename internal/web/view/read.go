package view

import (
	"context"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
)

// LogPage is one read of a logs view.
type LogPage struct {
	Entries []store.Entry // ascending
	HasMore bool
	// NextSeq continues forward (following, or the next page of a range).
	NextSeq int64
	// NextBeforeSeq pages backwards from the oldest entry shown.
	NextBeforeSeq int64
}

// ReadLogs reads one page of f over ids:
//   - from_seq or since_seq: forward from there (to to_seq);
//   - before_seq: the newest entries before it;
//   - neither: the newest entries (up to to_seq), and a cursor that
//     follows from the end.
func ReadLogs(ctx context.Context, st *store.Store, f LogFilter, ids []string, limit int) (LogPage, error) {
	q := f.Query(ids)
	q.Limit = limit
	start := max(f.FromSeq, f.SinceSeq)
	if start > 0 {
		q.SinceSeq = start
		res, err := st.Read(ctx, q)
		if err != nil {
			return LogPage{}, err
		}
		return page(res, false), nil
	}
	// Taken before reading, so the follow cursor can only repeat an entry,
	// never skip one committed during the read.
	last, err := st.LastSeq(ctx)
	if err != nil {
		return LogPage{}, err
	}
	q.Desc = true
	q.BeforeSeq = f.BeforeSeq
	res, err := st.Read(ctx, q)
	if err != nil {
		return LogPage{}, err
	}
	p := page(res, true)
	switch {
	case f.BeforeSeq > 0:
		p.NextSeq = f.BeforeSeq
	case f.ToSeq > 0:
		p.NextSeq = f.ToSeq + 1
	default:
		p.NextSeq = last + 1
	}
	p.HasMore = false // nothing newer than "the newest"
	if res.HasMore && len(res.Entries) > 0 {
		p.NextBeforeSeq = res.Entries[0].Seq
	}
	return p, nil
}

func page(res store.Result, desc bool) LogPage {
	p := LogPage{Entries: res.Entries, HasMore: res.HasMore, NextSeq: res.NextSeq}
	if !desc && len(res.Entries) > 0 && res.Entries[0].Seq > 1 {
		p.NextBeforeSeq = res.Entries[0].Seq
	}
	return p
}

// Counter is what messaging may implement to fill the chat badges of
// Counts. Until it does, only sources_down is counted.
type Counter interface {
	Counts(ctx context.Context, viewer auth.Principal) (dto.Counts, error)
}

// Presenter is what messaging may implement to turn a message entry into
// its full DTO for a viewer (segments, chat id from the viewer's side,
// attachments). Without it a message gets its text as one segment.
type Presenter interface {
	Present(ctx context.Context, viewer auth.Principal, e store.Entry) dto.Entry
}

// Counts are the badges for viewer.
func Counts(ctx context.Context, d deps.Deps, viewer auth.Principal) dto.Counts {
	var c dto.Counts
	if counter, ok := d.Messaging.(Counter); ok && d.Messaging != nil {
		if got, err := counter.Counts(ctx, viewer); err == nil {
			c = got
		}
	}
	c.SourcesDown = 0
	if d.Status != nil {
		a := d.Projects.Access(ctx, viewer)
		for _, s := range d.Status.Snapshot() {
			if s.Status == dto.StreamDown && !store.IsMessageStream(s.ID) && a.Can(s.ID, auth.ScopeRead) {
				c.SourcesDown++
			}
		}
	}
	return c
}

// Present is the DTO of any entry for viewer: messaging's presenter for
// messages when there is one.
func Present(ctx context.Context, d deps.Deps, kinds *Kinds, viewer auth.Principal, e store.Entry) dto.Entry {
	if e.Kind == store.KindMessage && d.Messaging != nil {
		if p, ok := d.Messaging.(Presenter); ok {
			return p.Present(ctx, viewer, e)
		}
		out := Entry(ctx, e, kinds)
		out.Chat = d.Messaging.ChatOf(e)
		return out
	}
	return Entry(ctx, e, kinds)
}
