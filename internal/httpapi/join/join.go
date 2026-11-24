// Package join is /join/{code} on the public listener: where a one-time link
// is used (docs/design.md, "Creating an identity").
//
//	GET  /join/{code}        a person's link: a page with a Sign in button.
//	                         a phone's QR code: signs in, and opens the chat.
//	                         an agent's link, from a browser: a page with the
//	                         two commands that run it.
//	                         an agent's link, from a shell: the install
//	                         script, carrying the agent's token, for
//	                         `curl -fsSL '…/join/{code}?shell=sh' | sh` or
//	                         `irm '…/join/{code}?shell=ps1' | iex`.
//	POST /join/{code}        the Sign in button: signs this browser in, and
//	                         opens the chat.
//	POST /join/{code}/token  the agent's token, once, for the scripts handed
//	                         out before the script carried it.
//
// The pages hold no secret: the code is in the URL already. Every route is
// rate-limited per address, so a code cannot be guessed at speed; at 256
// bits it could not be anyway.
//
// A link sent in a message is fetched by the messenger's preview before
// anyone taps it, so no browser spends a link by opening it: a person's is
// used by the button, and an agent's only by a shell — curl, wget or
// PowerShell, which no preview is. The phone's QR code is scanned, never
// sent, and lasts two minutes: opening it is enough.
package join

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cafard/internal/httpapi/httpx"
	"cafard/internal/i18n"
	"cafard/internal/install"
	"cafard/internal/ratelimit"
	"cafard/internal/service/access"
	"cafard/internal/store"
)

type Join struct {
	log       *slog.Logger
	acc       *access.Service
	publicURL *httpx.PublicURL
	limit     *ratelimit.Limiter
}

// New serves join links. publicURL is where agents reach this listener, as
// written into the install script.
func New(log *slog.Logger, acc *access.Service, publicURL *httpx.PublicURL) *Join {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Join{log: log, acc: acc, publicURL: publicURL, limit: ratelimit.New(30, time.Minute)}
}

func (j *Join) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /join/{code}", j.limited(j.open))
	mux.HandleFunc("POST /join/{code}", j.limited(j.signInPerson))
	mux.HandleFunc("POST /join/{code}/token", j.limited(j.token))
}

func clientIP(r *http.Request) string { return httpx.ClientIP(r) }

func (j *Join) limited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := j.limit.Allow(clientIP(r)); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			http.Error(w, say(r, "Too many attempts from this address. Wait a minute."), http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

// shellOf is the shell a request is from — "sh" for curl or wget, "ps1" for
// PowerShell's irm, whose user agent starts Mozilla/5.0 like a browser's but
// names it — or "" for a browser, which gets a page. The query says which
// script a shell wants; without one, as in the commands made before there
// were two, curl and wget want sh.
func shellOf(r *http.Request) string {
	ua := r.UserAgent()
	def := ""
	switch {
	case strings.Contains(ua, "PowerShell"):
		def = "ps1"
	case strings.HasPrefix(ua, "curl/"), strings.HasPrefix(ua, "Wget/"):
		def = "sh"
	default:
		return ""
	}
	if q := r.URL.Query().Get("shell"); q != "" {
		return q
	}
	return def
}

func (j *Join) open(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	l, id, err := j.acc.LinkFor(r.Context(), code)
	if err != nil {
		j.refused(w, r, err)
		return
	}
	shell := shellOf(r)
	if l.Kind == store.AgentLink {
		if shell == "" {
			// The client is chosen by following a link to this page with
			// ?client=: the page has no script.
			c, err := ParseClient(r.URL.Query().Get("client"))
			if err != nil {
				c = Clients[0]
			}
			link := j.publicURL.String() + "/join/" + code
			title := say(r, "Set up %s", id.Name)
			d := pageData{Title: title, Heading: title, Expires: l.ExpiresAt, Agent: true,
				Sh: install.Command(link, false, c.ID), PS1: install.Command(link, true, c.ID)}
			for _, o := range Clients {
				d.Clients = append(d.Clients, pageClient{Name: o.Name, Href: "?client=" + o.ID, On: o == c})
			}
			page(w, r, http.StatusOK, d)
			return
		}
		j.script(w, r, code, shell)
		return
	}
	if shell != "" {
		shellError(w, shell, "This link is for "+id.Name+", a person: open it in a browser.")
		return
	}
	if l.Kind == store.SigninLink {
		j.signIn(w, r, code)
		return
	}
	page(w, r, http.StatusOK, pageData{Title: say(r, "Sign in"), Heading: say(r, "Sign in as %s", displayName(id)),
		Username: id.Name, Expires: l.ExpiresAt})
}

// signIn is a phone that scanned the code shown by "Sign in another phone":
// it is signed in to the chat app and sent there. Opening the link is the
// whole of it: the code was on the screen of a phone signed in as the
// person, two minutes ago at most.
func (j *Join) signIn(w http.ResponseWriter, r *http.Request, code string) {
	_, cookie, err := j.acc.RedeemSigninLink(r.Context(), code, clientIP(r), r.UserAgent())
	if err != nil {
		j.refused(w, r, err)
		return
	}
	httpx.SetChatCookie(w, cookie, httpx.IsHTTPS(r))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// say is format in the language of the browser asking (internal/i18n). The
// scripts and the errors served to a shell are not said with it: a terminal
// is answered in English.
func say(r *http.Request, format string, args ...any) string {
	return i18n.T(i18n.FromRequest(r), format, args...)
}

func displayName(id store.Identity) string {
	if id.DisplayName != "" {
		return id.DisplayName
	}
	return id.Name
}

// signInPerson is the Sign in button of a person's link.
func (j *Join) signInPerson(w http.ResponseWriter, r *http.Request) {
	// The form posts back to its own URL. A post from another site would need
	// the code, which is the secret; refusing cross-site posts as well costs
	// nothing.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, say(r, "Cross-site requests are refused."), http.StatusForbidden)
		return
	}
	_, cookie, err := j.acc.SignInWithLink(r.Context(), r.PathValue("code"), clientIP(r), r.UserAgent())
	if err != nil {
		j.refused(w, r, err)
		return
	}
	httpx.SetChatCookie(w, cookie, httpx.IsHTTPS(r))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// script redeems an agent's link and serves the script that sets the
// machine up with its token: the token exists from this moment, and only in
// this response.
func (j *Join) script(w http.ResponseWriter, r *http.Request, code, shell string) {
	if shell != "sh" && shell != "ps1" {
		shellError(w, "sh", "shell="+shell+" is not one cafard writes for: use sh or ps1.")
		return
	}
	// The client is read before the link is spent: a mistyped one costs
	// nothing but running the command again.
	client, err := ParseClient(r.URL.Query().Get("client"))
	if err != nil {
		shellError(w, shell, err.Error())
		return
	}
	id, token, err := j.acc.RedeemAgentLink(r.Context(), code, clientIP(r))
	if err != nil {
		j.refused(w, r, err)
		return
	}
	j.log.Info("agent link redeemed", "agent", id.Name, "from", clientIP(r), "shell", shell, "client", client.ID)
	b := agentBundle(j.publicURL.String(), id.Name, token, client)
	if err := b.Validate(); err != nil {
		// What agentBundle writes is fixed, so this is a bug; the link is
		// spent, and an admin makes another once it is fixed.
		j.log.Error("install bundle", "err", err)
		shellError(w, shell, "cafard could not write the script; the error is in its log.")
		return
	}
	if shell == "ps1" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, install.PowerShell(b))
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	fmt.Fprint(w, install.Shell(b))
}

// token is the exchange the scripts served before this one made: they post
// the code and keep what comes back.
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
		ref = access.Refuse("", "cafard could not do that; the error is in its log.")
	}
	// A shell is answered in English, as a terminal is everywhere else.
	if shell := shellOf(r); shell != "" {
		shellError(w, shell, ref.Message)
		return
	}
	title := say(r, "This link no longer works")
	page(w, r, http.StatusGone, pageData{Title: title, Heading: title, Error: ref.In(i18n.FromRequest(r)), Spent: true})
}

// shellError is a script that says why and fails, so `curl … | sh` and
// `irm … | iex` print a reason rather than running nothing.
//
// It is served as 200 whatever went wrong. The sh command is `curl -fsSL`,
// and -f makes curl print nothing at all for an error status: sh would get
// an empty script, and the person would see only curl's "returned error:
// 410". irm would throw on one before iex ran anything. The script's own
// failure is the failure.
func shellError(w http.ResponseWriter, shell, msg string) {
	if shell == "ps1" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, install.Failure(shell == "ps1", msg))
}
