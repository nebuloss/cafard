package pages

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
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
	Open           bool
	Active         int
	Streams, Re    string
	Known          []string
	Severities     []sevOption
	Lines, Markers bool
	FromSeq, ToSeq string
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
}

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

	if apiErr == nil {
		var patterns []string
		if patterns, apiErr = f.Patterns(ctx, s.d); apiErr == nil {
			ids, err := view.StreamIDs(ctx, s.d.Store, patterns)
			if err != nil {
				s.Oops(w, r, &p, err)
				return
			}
			pg, err := view.ReadLogs(ctx, s.d.Store, f, ids, LiveLines)
			if err != nil {
				s.Oops(w, r, &p, err)
				return
			}
			data.Items = LogItems(pg.Entries)
			data.NextSeq = pg.NextSeq
			data.Follow = f.ToSeq == 0
			data.Showing = showing(f, len(pg.Entries))
		}
	}
	if apiErr != nil {
		status = dto.HTTPStatus(apiErr.Code)
		data.Error = apiErr.Message
		data.Follow = false
		data.Showing = "nothing"
	}
	data.Query = f.URL()
	s.Render(w, status, "live", s.Shell(r, &p, "Live", "live", "live"), []string{"tpl-line", "tpl-marker", "tpl-toast"}, data)
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
	}
	if f.FromSeq > 0 {
		v.FromSeq = strconv.FormatInt(f.FromSeq, 10)
	}
	if f.ToSeq > 0 {
		v.ToSeq = strconv.FormatInt(f.ToSeq, 10)
	}
	for _, on := range []bool{v.Streams != "", v.Re != "", sev != "7", !v.Lines || !v.Markers} {
		if on {
			v.Active++
		}
	}
	return v
}
