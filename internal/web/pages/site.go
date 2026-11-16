// Package pages serves the server-rendered pages and their form posts.
// Every page works without the bundle; the bundle only enhances it.
//
// Track C owns the shell and the pages of this file set; track D adds its
// pages (home, chat, drop) in files of its own, through AddTwin and the
// same render and auth helpers.
package pages

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/web/assets"
	"github.com/nebuloss/cafard/internal/web/view"
)

// Site holds what every page handler needs.
type Site struct {
	d        deps.Deps
	sessions *auth.Sessions
	pairings *auth.Pairings
	kinds    *view.Kinds
	log      *slog.Logger

	pages map[string]*template.Template // by file, "pages/streams.html"
	twins map[string]twin               // by id, "tpl-line"

	chats *chatSite // track D's pages; nil without messaging
}

// twin is a partial rendered with zero values inside <template id=...>.
type twin struct {
	tmpl string
	zero any
}

// New parses the templates: one copy of the whole set per page, in which
// "content" is that page's body.
func New(d deps.Deps, sessions *auth.Sessions, pairings *auth.Pairings, kinds *view.Kinds) (*Site, error) {
	root, err := assets.Templates(template.FuncMap{})
	if err != nil {
		return nil, err
	}
	s := &Site{d: d, sessions: sessions, pairings: pairings, kinds: kinds, log: d.Log,
		pages: map[string]*template.Template{}, twins: map[string]twin{}}
	if s.log == nil {
		s.log = slog.Default()
	}
	for _, t := range root.Templates() {
		name := t.Name()
		if !strings.HasPrefix(name, "pages/") {
			continue
		}
		clone, err := root.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.New("content").Parse(`{{template "` + name + `" .}}`); err != nil {
			return nil, err
		}
		s.pages[name] = clone
	}
	s.AddTwin("tpl-line", "partials/line.html", LineView{})
	s.AddTwin("tpl-marker", "partials/marker.html", MarkerView{})
	s.AddTwin("tpl-stream-dot", "partials/stream_dot.html", StreamDotView{})
	s.AddTwin("tpl-stream-card", "partials/stream_card.html", StreamCardView{})
	s.AddTwin("tpl-toast", "partials/toast.html", assets.Toast{})
	return s, nil
}

// AddTwin declares a <template> twin: id, the partial that renders it, and
// the zero value it is rendered with.
func (s *Site) AddTwin(id, tmpl string, zero any) { s.twins[id] = twin{tmpl: tmpl, zero: zero} }

// Twins lists the declared twin ids, for the template test.
func (s *Site) Twins() map[string]string {
	out := map[string]string{}
	for id, t := range s.twins {
		out[id] = t.tmpl
	}
	return out
}

// Page is the data every page template gets: the shell, and the page's own
// data under .Data.
type Page struct {
	assets.Layout
	Data any
}

// Shell builds the layout for a page. p is nil when signed out.
func (s *Site) Shell(r *http.Request, p *auth.Principal, title, page, nav string) assets.Layout {
	l := assets.NewLayout(title, page)
	l.Theme = string(view.ThemeOf(r))
	l.Nav = nav
	l.Path = r.URL.RequestURI()
	if p != nil {
		l.User = &assets.User{Name: p.Name, Kind: p.Kind, Admin: p.Has(auth.ScopeAdmin), Initial: initial(p.Name)}
		c := view.Counts(r.Context(), s.d, *p)
		l.Badges = assets.Badges{Chats: c.OpenQuestions + c.UnreadDirect, SourcesDown: c.SourcesDown > 0}
	}
	return l
}

func initial(name string) string {
	r, _ := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return "?"
	}
	return string(unicode.ToUpper(r))
}

// Render writes page (its file under pages/, without extension) with the
// twins it lists.
func (s *Site) Render(w http.ResponseWriter, status int, page string, l assets.Layout, twins []string, data any) {
	t, ok := s.pages["pages/"+page+".html"]
	if !ok {
		http.Error(w, "no such page template", http.StatusInternalServerError)
		return
	}
	var tw bytes.Buffer
	for _, id := range twins {
		tdef, ok := s.twins[id]
		if !ok {
			http.Error(w, "no such twin "+id, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(&tw, "<template id=%q>", id)
		if err := t.ExecuteTemplate(&tw, tdef.tmpl, tdef.zero); err != nil {
			s.log.Error("rendering a template twin failed", "twin", id, "err", err)
			http.Error(w, "rendering failed", http.StatusInternalServerError)
			return
		}
		tw.WriteString("</template>\n")
	}
	// The partials are this package's own templates, escaped by
	// html/template when they were executed above.
	l.Twins = template.HTML(tw.String())
	var body bytes.Buffer
	if err := t.ExecuteTemplate(&body, "layout", Page{Layout: l, Data: data}); err != nil {
		s.log.Error("rendering a page failed", "page", page, "err", err)
		http.Error(w, "rendering failed", http.StatusInternalServerError)
		return
	}
	SecurityHeaders(w.Header())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

// SecurityHeaders are set on every page. Pages hold personal data, so
// nothing is cached; nothing inline runs, so the policy can be strict.
func SecurityHeaders(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; "+
		"connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
}

// Session is the principal of the request's session cookie. Pages take
// sessions only: a bearer token is for programs.
func (s *Site) Session(r *http.Request) (auth.Principal, bool) {
	c, err := r.Cookie(auth.SessionCookie)
	if err != nil || c.Value == "" {
		return auth.Principal{}, false
	}
	p, err := s.sessions.Lookup(r.Context(), c.Value)
	if err != nil {
		return auth.Principal{}, false
	}
	return p, true
}

// Handler is a page handler with its principal.
type Handler func(w http.ResponseWriter, r *http.Request, p auth.Principal)

// Auth wraps h: no session goes to /signin (and back after), a missing
// scope is a 403 page. An empty scope only needs a session.
func (s *Site) Auth(scope auth.Scope, h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.Session(r)
		if !ok {
			target := "/signin"
			if r.Method == http.MethodGet && r.URL.Path != "/" {
				target += "?next=" + url.QueryEscape(r.URL.RequestURI())
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		if scope != "" && !p.Has(scope) {
			s.Fail(w, r, &p, http.StatusForbidden, "Not allowed", auth.MissingScope(scope).Message)
			return
		}
		h(w, r, p)
	}
}

// Write is Auth for a form post: same-origin only (the whole CSRF defence
// of a cookie session).
func (s *Site) Write(scope auth.Scope, h Handler) http.Handler {
	return auth.RequireSameOrigin(s.Auth(scope, h))
}

type errorData struct{ Heading, Message string }

// Fail renders the error page.
func (s *Site) Fail(w http.ResponseWriter, r *http.Request, p *auth.Principal, status int, heading, message string) {
	s.Render(w, status, "error", s.Shell(r, p, heading, "", ""), []string{"tpl-toast"}, errorData{Heading: heading, Message: message})
}

// Oops is Fail for an unexpected error, which is logged, not shown.
func (s *Site) Oops(w http.ResponseWriter, r *http.Request, p *auth.Principal, err error) {
	if !errors.Is(err, context.Canceled) {
		s.log.Error("page failed", "path", r.URL.Path, "err", err)
	}
	s.Fail(w, r, p, http.StatusInternalServerError, "Something went wrong", "The server could not build this page. Its log says why.")
}

// SafeNext is a same-origin path to go back to, or "".
func SafeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return ""
	}
	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return ""
	}
	return next
}

// Mount registers track C's pages. Track D's pages mount themselves next
// to these; home is in home.go, a placeholder D replaces.
func (s *Site) Mount(mux *http.ServeMux) {
	s.mountAccount(mux)
	s.mountStreams(mux)
	s.mountAgents(mux)
	s.mountPair(mux)
	s.mountLive(mux)
	s.mountHome(mux)
}
