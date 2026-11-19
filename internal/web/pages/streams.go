package pages

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/web/view"
)

func (s *Site) mountStreams(mux *http.ServeMux) {
	mux.HandleFunc("GET /streams", s.Auth(auth.ScopeRead, s.streams))
}

// StreamGroupView is the sources of one project on Sources; Project is
// "" for those outside projects.
type StreamGroupView struct {
	Project, Label string
	Streams        []StreamCardView
}

type streamsData struct {
	Groups   []StreamGroupView
	Unrouted string // for the admin: what reached no sink, by transport
	Commands []dto.Snippet
}

func (s *Site) streams(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	list, err := view.VisibleStreams(r.Context(), s.d, p)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data := streamsData{}
	if data.Groups, err = s.streamGroups(r.Context(), list); err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	if len(data.Groups) == 0 {
		data.Commands = view.SourceCommands(s.d.Server.BaseURL())
	}
	if p.Has(auth.ScopeAdmin) {
		data.Unrouted = unroutedText(s.d.Status.UnroutedCounts())
	}
	s.Render(w, http.StatusOK, "streams", s.Shell(r, &p, "Sources", "streams", "streams"),
		[]string{"tpl-stream-dot", "tpl-stream-card", "tpl-toast"}, data)
}

// streamGroups sorts sources into their projects, by name, with those
// outside projects last.
func (s *Site) streamGroups(ctx context.Context, list []dto.Stream) ([]StreamGroupView, error) {
	labels := map[string]string{"": "Outside projects"}
	all, err := s.d.Projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	for _, pr := range all {
		labels[pr.Name] = pr.Label()
	}
	now := s.d.Clock.Now()
	byProject := map[string]*StreamGroupView{}
	var order []string
	for _, st := range list {
		g := byProject[st.Project]
		if g == nil {
			label, ok := labels[st.Project]
			if !ok {
				label = st.Project
			}
			g = &StreamGroupView{Project: st.Project, Label: label}
			byProject[st.Project] = g
			order = append(order, st.Project)
		}
		g.Streams = append(g.Streams, StreamCard(st, now))
	}
	sort.Slice(order, func(i, j int) bool { return order[j] == "" || (order[i] != "" && order[i] < order[j]) })
	out := make([]StreamGroupView, len(order))
	for i, name := range order {
		out[i] = *byProject[name]
	}
	return out, nil
}

// unroutedText is "syslog 31, http 2", or "" when nothing was dropped.
func unroutedText(counts map[string]int64) string {
	keys := make([]string, 0, len(counts))
	for k, n := range counts {
		if n > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, counts[k])
	}
	return strings.Join(parts, ", ")
}
