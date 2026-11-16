package pages

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/web/view"
)

func (s *Site) mountAccount(mux *http.ServeMux) {
	mux.HandleFunc("GET /signin", s.signinPage)
	mux.Handle("POST /signin", auth.RequireSameOrigin(http.HandlerFunc(s.signin)))
	mux.Handle("POST /signout", s.Write("", s.signout))
	mux.HandleFunc("GET /p/{code}", s.redeem)
	mux.Handle("POST /theme", s.Write("", s.theme))
	mux.Handle("POST /sessions/{id}/revoke", s.Write("", s.revokeSession))
}

type signinData struct {
	Next  string
	Error string
}

func (s *Site) renderSignin(w http.ResponseWriter, r *http.Request, status int, data signinData) {
	s.Render(w, status, "signin", s.Shell(r, nil, "Sign in", "signin", ""), []string{"tpl-toast"}, data)
}

func (s *Site) signinPage(w http.ResponseWriter, r *http.Request) {
	next := SafeNext(r.URL.Query().Get("next"))
	if _, ok := s.Session(r); ok {
		if next == "" {
			next = "/"
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	s.renderSignin(w, r, http.StatusOK, signinData{Next: next})
}

// signin trades a pasted token for a session. The token is never echoed
// back, not even into the form after a mistake.
func (s *Site) signin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	token := strings.TrimSpace(r.PostFormValue("token"))
	next := SafeNext(r.PostFormValue("next"))
	if token == "" {
		s.renderSignin(w, r, http.StatusBadRequest, signinData{Next: next, Error: "Paste a token to sign in."})
		return
	}
	p, err := s.d.Tokens.Lookup(r.Context(), token)
	if err != nil {
		msg := "This token is not valid. It may have been revoked, or a character is missing."
		if strings.HasPrefix(token, auth.SessionPrefix) {
			msg = "That is a session cookie, not a token. Paste a token that starts with cfd_."
		}
		s.renderSignin(w, r, http.StatusUnauthorized, signinData{Next: next, Error: msg})
		return
	}
	if p.Kind != auth.KindHuman {
		s.renderSignin(w, r, http.StatusForbidden, signinData{Next: next,
			Error: "This token belongs to " + p.Name + ", a " + p.Kind + ". Only a person's token opens a browser session."})
		return
	}
	_, secret, err := s.sessions.Create(r.Context(), p.Name, p.Scopes, auth.SessionLabel(r.UserAgent()), r.UserAgent())
	if err != nil {
		s.Oops(w, r, nil, err)
		return
	}
	auth.SetSessionCookie(w, secret, s.d.Server.Secure)
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Site) signout(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if err := s.sessions.Revoke(r.Context(), p.SessionID); err != nil && !errors.Is(err, auth.ErrNotFound) {
		s.Oops(w, r, &p, err)
		return
	}
	auth.ClearSessionCookie(w, s.d.Server.Secure)
	http.Redirect(w, r, "/signin", http.StatusSeeOther)
}

// redeem is the phone side of pairing: the QR's URL. GET because a camera
// app opens it as a link; the code works once, so a link preview that
// fetched it first would only make the phone show "already used".
func (s *Site) redeem(w http.ResponseWriter, r *http.Request) {
	_, secret, err := s.pairings.Redeem(r.Context(), r.PathValue("code"), r.UserAgent())
	if err != nil {
		msg := "This pairing code expired or was already used. Ask for a new one on the Pair page."
		if !errors.Is(err, auth.ErrPairingUsed) && !errors.Is(err, auth.ErrConflict) {
			s.log.Error("redeeming a pairing code failed", "err", err)
			msg = "The server could not redeem this code. Its log says why."
		}
		s.renderSignin(w, r, http.StatusBadRequest, signinData{Error: msg})
		return
	}
	auth.SetSessionCookie(w, secret, s.d.Server.Secure)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// theme stores the choice for this browser. A year, so a phone does not
// go light again a week later; the value is one of three words.
func (s *Site) theme(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	choice := dto.Theme(r.PostFormValue("theme"))
	switch choice {
	case dto.ThemeLight, dto.ThemeDark, dto.ThemeSystem:
	default:
		choice = dto.ThemeSystem
	}
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

// revokeSession signs a browser out: any session for an admin, one's own
// sessions for everyone else.
func (s *Site) revokeSession(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.Fail(w, r, &p, http.StatusNotFound, "Not found", "There is no such session.")
		return
	}
	row, err := s.sessions.Get(r.Context(), id)
	if err != nil || (!p.Has(auth.ScopeAdmin) && row.Principal != p.Name) {
		s.Fail(w, r, &p, http.StatusNotFound, "Not found", "There is no such session.")
		return
	}
	if err := s.sessions.Revoke(r.Context(), id); err != nil && !errors.Is(err, auth.ErrNotFound) {
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
