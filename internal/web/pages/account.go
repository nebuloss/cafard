package pages

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/web/view"
)

func (s *Site) mountAccount(public, admin *http.ServeMux) {
	public.HandleFunc("GET /signin", s.signinPage(false))
	// Not the 405 the GET above would earn it: on the public interface the
	// form does not exist, and saying so would point at the admin one.
	public.HandleFunc("POST /signin", http.NotFound)
	public.Handle("POST /signout", s.Write("", s.signout))
	public.HandleFunc("GET /p/{code}", s.redeem)
	public.Handle("POST /theme", s.Write("", s.theme))
	public.Handle("POST /sessions/{id}/revoke", s.Write("", s.revokeSession))
	admin.HandleFunc("GET /signin", s.signinPage(true))
	admin.Handle("POST /signin", auth.RequireSameOrigin(http.HandlerFunc(s.signin)))
	admin.Handle("POST /password", s.Write(auth.ScopeAdmin, s.changePassword))
}

type signinData struct {
	Password bool // the admin's password form, on the admin interface only
	Next     string
	Error    string
}

func (s *Site) renderSignin(w http.ResponseWriter, r *http.Request, status int, data signinData) {
	l := s.Shell(r, nil, "Sign in", "signin", "")
	if r.URL.Query().Get("changed") == "1" {
		l.Notice = "Password changed. Sign in with the new one."
	}
	s.Render(w, status, "signin", l, []string{"tpl-toast"}, data)
}

// signinPage is the password form on the admin interface. On the public
// one there is nothing to type: people sign in with a link or QR code the
// admin makes.
func (s *Site) signinPage(password bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next := SafeNext(r.URL.Query().Get("next"))
		if _, ok := s.Session(r); ok {
			if next == "" {
				next = "/"
			}
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		s.renderSignin(w, r, http.StatusOK, signinData{Password: password, Next: next})
	}
}

// signin trades the admin password for an admin session. Every refusal of
// the password reads the same, whether a password is set or not.
func (s *Site) signin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	password := r.PostFormValue("password")
	next := SafeNext(r.PostFormValue("next"))
	data := signinData{Password: true, Next: next}
	if password == "" {
		data.Error = "Type the admin password."
		s.renderSignin(w, r, http.StatusBadRequest, data)
		return
	}
	secret, err := s.d.Admin.SignIn(r.Context(), remoteHost(r), password)
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		data.Error = "Too many attempts from this address. Wait a minute, then try again."
		s.renderSignin(w, r, http.StatusTooManyRequests, data)
		return
	case errors.Is(err, auth.ErrWrongPassword):
		data.Error = "This is not the admin password."
		s.renderSignin(w, r, http.StatusUnauthorized, data)
		return
	case err != nil:
		s.Oops(w, r, nil, err)
		return
	}
	auth.SetAdminCookie(w, secret, s.d.Server.Secure)
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// remoteHost is the rate limit's key: the peer's address without its port.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// signout ends the session the request came with: the admin one, which
// alone carries the admin scope, or the browser one.
func (s *Site) signout(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if p.Has(auth.ScopeAdmin) {
		if c, err := r.Cookie(auth.AdminCookie); err == nil {
			s.d.Admin.SignOut(c.Value)
		}
		auth.ClearAdminCookie(w, s.d.Server.Secure)
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := s.sessions.Revoke(r.Context(), p.SessionID); err != nil && !errors.Is(err, auth.ErrNotFound) {
		s.Oops(w, r, &p, err)
		return
	}
	auth.ClearSessionCookie(w, s.d.Server.Secure)
	http.Redirect(w, r, "/signin", http.StatusSeeOther)
}

// changePassword needs the current password even in an admin session, so
// a browser left open is not enough to lock the admin out. It ends every
// admin session, this one included.
func (s *Site) changePassword(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	current, next := r.PostFormValue("current"), r.PostFormValue("new")
	if next != r.PostFormValue("repeat") {
		s.renderAgents(w, r, p, http.StatusBadRequest, nil, "The two new passwords differ.")
		return
	}
	err := s.d.Admin.ChangePassword(r.Context(), current, next)
	switch {
	case errors.Is(err, auth.ErrShortPassword):
		s.renderAgents(w, r, p, http.StatusBadRequest, nil, sentence(err.Error()))
		return
	case errors.Is(err, auth.ErrWrongPassword):
		s.renderAgents(w, r, p, http.StatusUnauthorized, nil, "The current password is wrong.")
		return
	case err != nil:
		s.Oops(w, r, &p, err)
		return
	}
	auth.ClearAdminCookie(w, s.d.Server.Secure)
	http.Redirect(w, r, "/signin?changed=1", http.StatusSeeOther)
}

// redeem is the phone side of pairing: the QR's URL. GET because a camera
// app opens it as a link; the code works once, so a link preview that
// fetched it first would only make the phone show "already used".
func (s *Site) redeem(w http.ResponseWriter, r *http.Request) {
	_, secret, err := s.pairings.Redeem(r.Context(), r.PathValue("code"), r.UserAgent())
	if err != nil {
		msg := "This sign-in link expired or was already used. Ask the administrator for a new one."
		if !errors.Is(err, auth.ErrPairingUsed) && !errors.Is(err, auth.ErrConflict) {
			s.log.Error("redeeming a pairing code failed", "err", err)
			msg = "The server could not redeem this code. Its log says why."
		}
		s.renderSignin(w, r, http.StatusBadRequest, signinData{Password: auth.OnAdminInterface(r.Context()), Error: msg})
		return
	}
	auth.SetSessionCookie(w, secret, s.d.Server.Secure)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// theme stores the choice for this browser. A year, so a phone does not
// go light again a week later; the value is one of three words.
func (s *Site) theme(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	choice := view.ParseTheme(r.PostFormValue("theme"))
	http.SetCookie(w, &http.Cookie{
		Name: view.ThemeCookie, Value: string(choice), Path: "/",
		Expires: s.d.Clock.Now().Add(365 * 24 * time.Hour), SameSite: http.SameSiteLaxMode, Secure: s.d.Server.Secure,
		// Readable by the bundle on purpose: a display preference, not a secret.
		HttpOnly: false,
	})
	next := SafeNext(r.PostFormValue("next"))
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// revokeSession signs a browser out (view.RevokeSession says whose).
func (s *Site) revokeSession(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id, perr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	err := auth.ErrNotFound
	if perr == nil {
		err = view.RevokeSession(r.Context(), s.sessions, p, id)
	}
	if errors.Is(err, auth.ErrNotFound) {
		s.Fail(w, r, &p, http.StatusNotFound, "Not found", "There is no such session.")
		return
	}
	if err != nil {
		s.Oops(w, r, &p, err)
		return
	}
	if id == p.SessionID {
		auth.ClearSessionCookie(w, s.d.Server.Secure)
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	next := SafeNext(r.PostFormValue("next"))
	if next == "" {
		next = "/pair"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}
