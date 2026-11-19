package pages

import (
	"net/http"

	"github.com/nebuloss/cafard/internal/auth"
)

// The admin's menu (admin interface): what is not inside one project.

func (s *Site) mountAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", s.Auth(auth.ScopeAdmin, s.adminPage))
}

type adminData struct {
	Projects int
	Unrouted string // what reached no sink, by transport
}

func (s *Site) adminPage(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	all, err := s.d.Projects.Projects(r.Context())
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	data := adminData{Projects: len(all), Unrouted: unroutedText(s.d.Status.UnroutedCounts())}
	s.Render(w, http.StatusOK, "admin", s.Shell(r, &p, "Admin", "admin", "admin"), []string{"tpl-toast"}, data)
}
