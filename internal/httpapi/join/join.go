// Package join is /join/{code} on the public listener: where a one-time link
// is used (docs/design.md, "Creating an identity").
//
//	GET  /join/{code}        a person's link: a page to choose a password.
//	                         an agent's link: the install script, for
//	                         `curl -fsSL …/join/{code} | sh`.
//	POST /join/{code}        the password form.
//	POST /join/{code}/token  the install script exchanging the code for the
//	                         agent's token, once.
//
// The page and the script hold no secret: the code is in the URL already,
// and the token exists only from the moment the script asks for it. Every
// route is rate-limited per address, so a code cannot be guessed at speed;
// at 256 bits it could not be anyway, and the limit is for the password
// form behind it.
package join

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cafard/internal/httpapi/httpx"
	"cafard/internal/ratelimit"
	"cafard/internal/service/access"
	"cafard/internal/store"
)

type Join struct {
	log       *slog.Logger
	acc       *access.Service
	publicURL string
	limit     *ratelimit.Limiter
}

// New serves join links. publicURL is where agents reach this listener, as
// written into the install script.
func New(log *slog.Logger, acc *access.Service, publicURL string) *Join {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Join{log: log, acc: acc, publicURL: strings.TrimSuffix(publicURL, "/"), limit: ratelimit.New(30, time.Minute)}
}

func (j *Join) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /join/{code}", j.limited(j.open))
	mux.HandleFunc("POST /join/{code}", j.limited(j.choose))
	mux.HandleFunc("POST /join/{code}/token", j.limited(j.token))
}

func clientIP(r *http.Request) string { return httpx.ClientIP(r) }

func (j *Join) limited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := j.limit.Allow(clientIP(r)); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			http.Error(w, "Too many attempts from this address. Wait a minute.", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

// wantsScript is a request from curl or wget, which gets shell rather than a
// page: an agent's link is run, a person's is opened.
func wantsScript(r *http.Request) bool {
	ua := r.UserAgent()
	return strings.HasPrefix(ua, "curl/") || strings.HasPrefix(ua, "Wget/")
}

func (j *Join) open(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	l, id, err := j.acc.LinkFor(r.Context(), code)
	if err != nil {
		j.refused(w, r, err)
		return
	}
	if l.Kind == store.AgentLink {
		j.script(w, code, id)
		return
	}
	if wantsScript(r) {
		shellError(w, http.StatusBadRequest, "This link is for "+id.Name+", a person: open it in a browser.")
		return
	}
	if l.Kind == store.SigninLink {
		j.signIn(w, r, code)
		return
	}
	page(w, http.StatusOK, pageData{Title: "Choose a password", Name: displayName(id), Username: id.Name,
		Kind: l.Kind, Expires: l.ExpiresAt, Min: access.MinPassword})
}

// signIn is a phone that scanned the code shown by "Sign in another phone":
// it is signed in to the chat app and sent there. Opening the link is the
// whole of it, on purpose: the person proved who they are by giving their
// password on the device that showed the code, two minutes ago at most.
func (j *Join) signIn(w http.ResponseWriter, r *http.Request, code string) {
	_, cookie, err := j.acc.RedeemSigninLink(r.Context(), code, clientIP(r), r.UserAgent())
	if err != nil {
		j.refused(w, r, err)
		return
	}
	httpx.SetChatCookie(w, cookie, strings.HasPrefix(j.publicURL, "https://"))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func displayName(id store.Identity) string {
	if id.DisplayName != "" {
		return id.DisplayName
	}
	return id.Name
}

func (j *Join) choose(w http.ResponseWriter, r *http.Request) {
	// The form posts back to its own URL. A post from another site would need
	// the code, which is the secret; refusing cross-site posts as well costs
	// nothing.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, "Cross-site requests are refused.", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	code := r.PathValue("code")
	pass, again := r.PostForm.Get("password"), r.PostForm.Get("again")
	l, id, err := j.acc.LinkFor(r.Context(), code)
	if err != nil {
		j.refused(w, r, err)
		return
	}
	data := pageData{Title: "Choose a password", Name: displayName(id), Username: id.Name, Kind: l.Kind, Expires: l.ExpiresAt, Min: access.MinPassword}
	if pass != again {
		data.Error = "The two passwords are different."
		page(w, http.StatusBadRequest, data)
		return
	}
	if _, err := j.acc.ChoosePassword(r.Context(), code, pass, clientIP(r)); err != nil {
		if ref, ok := err.(*access.Refusal); ok && ref.Code == access.BadValue {
			data.Error = ref.Message
			page(w, http.StatusBadRequest, data)
			return
		}
		j.refused(w, r, err)
		return
	}
	page(w, http.StatusOK, pageData{Title: "You are in", Name: displayName(id), Username: id.Name, Done: true, Kind: l.Kind})
}

func (j *Join) token(w http.ResponseWriter, r *http.Request) {
	id, token, err := j.acc.RedeemAgentLink(r.Context(), r.PathValue("code"), clientIP(r))
	if err != nil {
		var msg string
		if ref, ok := err.(*access.Refusal); ok {
			msg = ref.Message
		} else {
			j.log.Error("redeem agent link", "err", err)
			msg = "cafard could not do that; the error is in its log."
		}
		http.Error(w, msg, http.StatusGone)
		return
	}
	j.log.Info("agent link redeemed", "agent", id.Name, "from", clientIP(r))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, token)
}

func (j *Join) refused(w http.ResponseWriter, r *http.Request, err error) {
	ref, ok := err.(*access.Refusal)
	if !ok {
		j.log.Error("join link", "err", err)
		ref = &access.Refusal{Message: "cafard could not do that; the error is in its log."}
	}
	if wantsScript(r) {
		shellError(w, http.StatusGone, ref.Message)
		return
	}
	page(w, http.StatusGone, pageData{Title: "This link no longer works", Error: ref.Message, Spent: true})
}

// shellError is a script that says why and fails, so `curl … | sh` prints a
// reason rather than running nothing.
//
// It is served as 200 whatever went wrong. The install command is
// `curl -fsSL`, and -f makes curl print nothing at all for an error status:
// sh would get an empty script, and the person would see only curl's
// "returned error: 410". The script's own exit status is the failure.
func shellError(w http.ResponseWriter, _ int, msg string) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "#!/bin/sh\necho %s >&2\nexit 1\n", shellQuote("cafard: "+msg))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
