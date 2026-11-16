package pages

import (
	"net/http"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/web/view"
)

// Home is a placeholder: track D replaces this file with the chat list.

func (s *Site) mountHome(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.Auth(auth.ScopeRead, s.home))
}

type homeData struct{ MCP string }

func (s *Site) home(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	s.Render(w, http.StatusOK, "home", s.Shell(r, &p, "Chats", "home", "chats"), []string{"tpl-stream-dot", "tpl-toast"},
		homeData{MCP: view.MCPCommand(s.d.Server.BaseURL())})
}
