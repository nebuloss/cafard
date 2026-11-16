package pages

import (
	"net/http"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/web/view"
)

func (s *Site) mountStreams(mux *http.ServeMux) {
	mux.HandleFunc("GET /streams", s.Auth(auth.ScopeRead, s.streams))
}

type streamsData struct {
	Streams  []StreamCardView
	Commands []dto.Snippet
}

func (s *Site) streams(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	list, err := view.Streams(r.Context(), s.d.Store, s.d.Status)
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	now := s.d.Clock.Now()
	data := streamsData{}
	for _, st := range list {
		data.Streams = append(data.Streams, StreamCard(st, now))
	}
	if len(data.Streams) == 0 {
		data.Commands = view.SourceCommands(s.d.Server.BaseURL())
	}
	s.Render(w, http.StatusOK, "streams", s.Shell(r, &p, "Sources", "streams", "streams"),
		[]string{"tpl-stream-dot", "tpl-stream-card", "tpl-toast"}, data)
}
