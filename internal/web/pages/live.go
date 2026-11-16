package pages

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/view"
)

// LiveLines is how many lines /live renders without a range.
const LiveLines = 500

func (s *Site) mountLive(mux *http.ServeMux) {
	mux.HandleFunc("GET /live", s.Auth(auth.ScopeRead, s.live))
}

type sevOption struct {
	Value, Label string
	Selected     bool
}

type liveFilter struct {
	Open                     bool
	Active                   int
	Streams, Re              string
	Known                    []string
	Channel                  string
	Channels                 []ChatOption // by name; empty without messaging
	Severities               []sevOption
	Lines, Markers, Messages bool
	FromSeq, ToSeq           string
}

type liveData struct {
	Filter   liveFilter
	Error    string
	Items    []LogItem
	NextSeq  int64
	Follow   bool
	Query    string
	Showing  string
	Commands []dto.Snippet
	Drop     *QuickDropView // the quick drop a range is attached through; nil without messaging or the message scope
}

// liveTwins are the twins of /live: rows, and the quick drop's chips.
var liveTwins = []string{"tpl-line", "tpl-marker", "tpl-attachment-chip", "tpl-upload-status", "tpl-toast"}

var sevLabels = []sevOption{
	{"7", "All", false}, {"6", "Info and worse", false}, {"5", "Notice and worse", false},
	{"4", "Warning and worse", false}, {"3", "Error and worse", false}, {"2", "Critical and worse", false},
	{"1", "Alert and worse", false}, {"0", "Emergency", false},
}

func (s *Site) live(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ctx := r.Context()
	data := liveData{Follow: true}
	status := http.StatusOK
	f, apiErr := view.ParseLogFilter(r.URL.Query())
	data.Filter = filterView(r, f)

	streams, err := s.d.Store.Streams(ctx)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	for _, st := range streams {
		if !store.IsMessageStream(st.ID) {
			data.Filter.Known = append(data.Filter.Known, st.ID)
		}
	}
	if len(data.Filter.Known) == 0 {
		data.Commands = view.SourceCommands(s.d.Server.BaseURL())
	}
	if s.chats != nil {
		chats, err := s.chats.svc.Chats(ctx, p, messaging.ChatsQuery{All: true})
		if err != nil {
			s.Oops(w, r, &p, err)
			return
		}
		for _, c := range chats {
			if c.Kind == dto.ChatChannel && !c.Archived {
				data.Filter.Channels = append(data.Filter.Channels, ChatOption{ID: c.Name, Label: ChatName(c), Selected: c.Name == f.Channel})
			}
		}
		if p.Has(auth.ScopeMessage) {
			drop := s.dropView(chats, "", p, r.URL.RequestURI())
			data.Drop = &drop
		}
	}

	if apiErr == nil {
		var patterns []string
		if patterns, apiErr = f.Patterns(ctx, s.d); apiErr == nil {
			ids, err := view.StreamIDs(ctx, s.d.Store, patterns)
			if err != nil {
				s.Oops(w, r, &p, err)
				return
			}
			var pg view.LogPage
			if len(f.Kinds) > 0 {
				if pg, err = view.ReadLogs(ctx, s.d.Store, f, ids, LiveLines); err != nil {
					s.Oops(w, r, &p, err)
					return
				}
			} else if pg.NextSeq, err = s.followFrom(ctx, f); err != nil {
				s.Oops(w, r, &p, err)
				return
			}
			entries := pg.Entries
			if f.Messages && s.d.Messaging != nil {
				msgs, err := s.liveMessages(ctx, p, f)
				if err != nil {
					s.Oops(w, r, &p, err)
					return
				}
				entries = mergeBySeq(entries, msgs, LiveLines, f.FromSeq > 0)
			}
			data.Items = LiveItems(entries, p.Name)
			data.NextSeq = pg.NextSeq
			data.Follow = f.ToSeq == 0
			data.Showing = showing(f, len(entries))
		}
	}
	if apiErr != nil {
		status = dto.HTTPStatus(apiErr.Code)
		data.Error = apiErr.Message
		data.Follow = false
		data.Showing = "nothing"
	}
	data.Query = f.URL()
	s.Render(w, status, "live", s.Shell(r, &p, "Live", "live", "live"), liveTwins, data)
}

func showing(f view.LogFilter, n int) string {
	switch {
	case f.FromSeq > 0 && f.ToSeq > 0:
		return fmt.Sprintf("seq %d to %d", f.FromSeq, f.ToSeq)
	case f.FromSeq > 0:
		return fmt.Sprintf("the first %d lines from seq %d", LiveLines, f.FromSeq)
	case f.ToSeq > 0:
		return fmt.Sprintf("the last %d lines up to seq %d", LiveLines, f.ToSeq)
	}
	return fmt.Sprintf("the last %d lines", LiveLines)
}

// filterView reads the raw query back into the form, so a rejected value
// stays in its field to be fixed.
func filterView(r *http.Request, f view.LogFilter) liveFilter {
	q := r.URL.Query()
	v := liveFilter{Streams: strings.Join(q["streams"], ","), Re: q.Get("re")}
	sev := q.Get("sev")
	if f.MinSev != nil {
		sev = strconv.Itoa(*f.MinSev)
	}
	if sev == "" {
		sev = "7"
	}
	for _, o := range sevLabels {
		o.Selected = o.Value == sev
		v.Severities = append(v.Severities, o)
	}
	v.Lines, v.Markers = true, true
	if kinds := q["kinds"]; len(kinds) > 0 {
		joined := "," + strings.Join(kinds, ",") + ","
		v.Lines = strings.Contains(joined, ",line,")
		v.Markers = strings.Contains(joined, ",marker,")
		v.Messages = strings.Contains(joined, ",message,")
	}
	v.Channel = f.Channel
	if f.FromSeq > 0 {
		v.FromSeq = strconv.FormatInt(f.FromSeq, 10)
	}
	if f.ToSeq > 0 {
		v.ToSeq = strconv.FormatInt(f.ToSeq, 10)
	}
	for _, on := range []bool{v.Streams != "", v.Channel != "", v.Re != "", sev != "7", !v.Lines || !v.Markers || v.Messages} {
		if on {
			v.Active++
		}
	}
	return v
}

// followFrom is where a view with no log kinds (kinds=message only)
// follows from: after its range, or after the newest entry.
func (s *Site) followFrom(ctx context.Context, f view.LogFilter) (int64, error) {
	if f.ToSeq > 0 {
		return f.ToSeq + 1, nil
	}
	last, err := s.d.Store.LastSeq(ctx)
	return last + 1, err
}

// liveMessages are the messages of kinds=message the viewer may see: the
// channel's with channel=, else every chat's. Each passes
// Messaging.Visible, so a direct message shows to its two parties only.
func (s *Site) liveMessages(ctx context.Context, p auth.Principal, f view.LogFilter) ([]store.Entry, error) {
	patterns := view.MessagePatterns(f)
	ids, err := s.d.Store.MatchStreams(ctx, patterns)
	if err != nil {
		return nil, err
	}
	ids = slices.DeleteFunc(ids, func(id string) bool {
		return !store.IsMessageStream(id) || !s.d.Messaging.Visible(ctx, p, store.Entry{Stream: id, Kind: store.KindMessage})
	})
	if len(ids) == 0 {
		return nil, nil
	}
	q := store.Query{Streams: ids, Kinds: []store.Kind{store.KindMessage}, MinSeverity: f.MinSev, Pattern: f.Re,
		UntilSeq: f.ToSeq, Limit: LiveLines}
	if f.FromSeq > 0 {
		q.SinceSeq = f.FromSeq
	} else {
		q.Desc = true
	}
	res, err := s.d.Store.Read(ctx, q)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(res.Entries, func(e store.Entry) bool { return !s.d.Messaging.Visible(ctx, p, e) }), nil
}

// mergeBySeq merges two ascending runs and keeps limit entries: the first
// ones of a range that starts at from_seq, else the newest.
func mergeBySeq(a, b []store.Entry, limit int, first bool) []store.Entry {
	out := append(slices.Clone(a), b...)
	slices.SortFunc(out, func(x, y store.Entry) int { return cmp.Compare(x.Seq, y.Seq) })
	if len(out) > limit {
		if first {
			out = out[:limit]
		} else {
			out = out[len(out)-limit:]
		}
	}
	return out
}

// LiveItems are the rows of /live: lines, markers, and messages as lines.
func LiveItems(es []store.Entry, viewer string) []LogItem {
	out := make([]LogItem, 0, len(es))
	for _, e := range es {
		switch e.Kind {
		case store.KindMarker:
			m := Marker(e)
			out = append(out, LogItem{Marker: &m})
		case store.KindMessage:
			out = append(out, LogItem{Line: MessageLine(e, viewer)})
		default:
			out = append(out, LogItem{Line: Line(e)})
		}
	}
	return out
}
