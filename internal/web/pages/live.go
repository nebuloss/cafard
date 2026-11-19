package pages

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	Action                   string // where the form goes: /live, or a project's logs
	Fixed                    bool   // the page picks the streams
	Open                     bool
	Active                   int
	Streams, Re              string
	Known                    []string
	Channel                  string
	Channels                 []ChatGroup // by project; empty without messaging
	Severities               []sevOption
	Lines, Markers, Messages bool
	FromSeq, ToSeq           string
}

// ChatGroup is the channels of one project, an <optgroup> of a picker.
type ChatGroup struct {
	Label   string
	Options []ChatOption
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
	data, status, err := s.logsView(r.Context(), p, r.URL.Query(), "/live", r.URL.RequestURI(), false)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	s.Render(w, status, "live", s.Shell(r, &p, "Live", "live", adminNav(p)), liveTwins, data)
}

// adminNav is the navigation item of a global page: Admin for the admin,
// none for the others, who reach it from a link.
func adminNav(p auth.Principal) string {
	if p.Has(auth.ScopeAdmin) {
		return "admin"
	}
	return ""
}

// logsView is a logs view: the filters of q, the lines they select and
// the quick drop a range is attached through. The form posts to action;
// with fixed, q's streams come from the page (a project's sinks) and the
// form offers no stream or channel of its own.
func (s *Site) logsView(ctx context.Context, p auth.Principal, q url.Values, action, back string, fixed bool) (liveData, int, error) {
	data := liveData{Follow: true}
	status := http.StatusOK
	f, apiErr := view.ParseLogFilter(q)
	data.Filter = filterView(q, f, fixed)
	data.Filter.Action = action

	if !fixed {
		streams, err := s.d.Store.Streams(ctx)
		if err != nil {
			return data, 0, err
		}
		acc := s.d.Projects.Access(ctx, p)
		seen := map[string]bool{}
		for _, st := range streams {
			if !store.IsMessageStream(st.ID) && acc.Can(st.ID, auth.ScopeRead) {
				// A whole project's logs first, then its streams.
				if pr := store.ProjectOf(st.ID); pr != "" && !seen[pr] && acc.ProjectScopes(pr).Has(auth.ScopeRead) {
					seen[pr] = true
					data.Filter.Known = append(data.Filter.Known, "*:"+pr+"/*")
				}
				data.Filter.Known = append(data.Filter.Known, st.ID)
			}
		}
		if len(data.Filter.Known) == 0 {
			data.Commands = view.SourceCommands(s.d.Server.BaseURL())
		}
	}
	if s.chats != nil {
		chats, err := s.chats.svc.Chats(ctx, p, messaging.ChatsQuery{All: true})
		if err != nil {
			return data, 0, err
		}
		groups := map[string]int{}
		for _, c := range chats {
			if fixed || c.Kind != dto.ChatChannel || c.Archived {
				continue
			}
			name := strings.TrimPrefix(c.ID, "chan:")
			i, ok := groups[c.Project]
			if !ok {
				label := c.ProjectTitle
				if label == "" {
					label = c.Project
				}
				i = len(data.Filter.Channels)
				groups[c.Project] = i
				data.Filter.Channels = append(data.Filter.Channels, ChatGroup{Label: label})
			}
			g := &data.Filter.Channels[i]
			g.Options = append(g.Options, ChatOption{ID: name, Label: ChatName(c), Selected: name == f.Channel || c.Name == f.Channel})
		}
		if p.Has(auth.ScopeMessage) {
			drop := s.dropView(chats, "", p, back)
			data.Drop = &drop
		}
	}

	if apiErr == nil {
		var patterns []string
		if patterns, apiErr = f.Patterns(ctx, s.d, p); apiErr == nil {
			ids, err := view.StreamIDs(ctx, s.d.Store, s.d.Projects.Access(ctx, p), patterns)
			if err != nil {
				return data, 0, err
			}
			var pg view.LogPage
			if len(f.Kinds) > 0 {
				if pg, err = view.ReadLogs(ctx, s.d.Store, f, ids, LiveLines); err != nil {
					return data, 0, err
				}
			} else if pg.NextSeq, err = s.followFrom(ctx, f); err != nil {
				return data, 0, err
			}
			entries := pg.Entries
			if f.Messages && s.d.Messaging != nil {
				msgs, err := s.liveMessages(ctx, p, f)
				if err != nil {
					return data, 0, err
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
	return data, status, nil
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
func filterView(q url.Values, f view.LogFilter, fixed bool) liveFilter {
	v := liveFilter{Streams: strings.Join(q["streams"], ","), Re: q.Get("re"), Fixed: fixed}
	if fixed {
		v.Streams = ""
	}
	var sev string
	v.Severities, sev = sevOptions(q.Get("sev"), f)
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

// sevOptions are the severity choices with the filter's one selected, or
// the raw value when the filter rejected it; sev is the selected value.
func sevOptions(raw string, f view.LogFilter) (opts []sevOption, sev string) {
	sev = raw
	if f.MinSev != nil {
		sev = strconv.Itoa(*f.MinSev)
	}
	if sev == "" {
		sev = "7"
	}
	for _, o := range sevLabels {
		o.Selected = o.Value == sev
		opts = append(opts, o)
	}
	return opts, sev
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
	patterns, err := view.MessagePatterns(ctx, s.d, p, f)
	if err != nil {
		return nil, nil
	}
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
