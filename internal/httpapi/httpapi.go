// Package httpapi puts cafard's HTTP surfaces together. It is the composition
// root: it builds what each surface needs and hands it over, and holds no
// logic of its own beyond that.
//
// There are two surfaces, on two listeners (internal/config):
//
//   - Public, published in the DMZ: the chat app, /api/v1, /mcp and join
//     links, as they land. It answers 404 to every /admin path, whatever the
//     proxy in front does, so the DMZ can never reach a route that creates a
//     person, an agent, a device or a project.
//   - Admin, on the internal network: the admin UI and /admin/api.
//
// Each is its own http.Handler, built from its own mux. Nothing is shared
// between them that could route a request from one to the other.
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"cafard/internal/httpapi/admin"
	"cafard/internal/httpapi/api"
	"cafard/internal/httpapi/httpx"
	"cafard/internal/httpapi/join"
	"cafard/internal/live"
	"cafard/internal/service/access"
	"cafard/internal/service/chat"
	"cafard/internal/store"
	"cafard/internal/version"
	"cafard/internal/webui"
)

// Deps is what the surfaces are built from. A test may leave the services
// out to check only the composition: the surfaces that need them are then
// not mounted.
type Deps struct {
	Log    *slog.Logger
	Store  *store.Store
	Access *access.Service
	Chat   *chat.Service
	// Hub is where /api/v1/events hears what changed.
	Hub *live.Hub
	// Admin is what the admin UI shows of the configuration; its PublicURL
	// is also where join links point.
	Admin admin.Config
}

type Server struct {
	public, admin http.Handler
}

func New(d Deps) *Server {
	return &Server{public: publicMux(d), admin: adminMux(d)}
}

// Public is the handler for the public listener.
func (s *Server) Public() http.Handler { return s.public }

// Admin is the handler for the admin listener.
func (s *Server) Admin() http.Handler { return s.admin }

func publicMux(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	if d.Access != nil && d.Chat != nil {
		api.New(d.Log, d.Access, d.Chat, d.Hub, d.Admin.PublicURL).Register(mux)
	}
	if d.Access != nil {
		join.New(d.Log, d.Access, d.Admin.PublicURL).Register(mux)
	}
	// The chat app, at the root of the public listener. blob: images and
	// media are the previews of photos chosen before they are sent; data:
	// images are the QR code of "Sign in another phone".
	mux.Handle("/", httpx.SPA(webui.Chat(), chatPolicy,
		"cafard: this binary was built without its chat app. Build it with `make build`, which runs `make web` first.\nThe API is at /api/v1.\n"))
	return secure(noAdmin(mux))
}

func adminMux(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	if d.Store != nil && d.Access != nil {
		admin.New(d.Log, d.Store, d.Access, d.Admin, webui.Admin()).Register(mux)
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte("cafard admin\n"))
		})
	}
	return secure(mux)
}

const chatPolicy = "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self'; script-src 'self'; " +
	"connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// noAdmin answers 404 to every /admin path before any route sees it.
//
// The public mux registers no admin route today, so this is a second lock
// rather than the only one: a route added to the wrong mux later, or a mux
// shared by mistake, still cannot answer an /admin path from the DMZ.
// The path is matched after cleaning, so /x/../admin is caught too.
func noAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.ToLower(cleanPath(r.URL.Path))
		if p == "/admin" || strings.HasPrefix(p, "/admin/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// secure sets the headers every answer carries.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok " + version.Version + "\n"))
}

// cleanPath resolves . and .. the way ServeMux does before it routes.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	var out []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, seg)
		}
	}
	return "/" + strings.Join(out, "/")
}
