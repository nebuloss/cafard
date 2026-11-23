// Package httpx is what more than one HTTP surface needs: the client's
// address, the cross-site check, and the chat app's sign-in cookie, which
// /api/v1 reads and /join sets when a phone signs in by QR code. The
// surfaces do not import one another; what they share is here.
package httpx

import (
	"net"
	"net/http"
	"net/url"
	"time"
)

// ClientIP is the address a request came from. Behind a proxy that is the
// proxy; trusting X-Forwarded-For from a configured proxy comes with the
// deployment step, as in claudication.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// SameOrigin reports whether a request may write with a cookie: it comes
// from this site's own pages. Sec-Fetch-Site says so where the browser
// sends it; where it does not, a browser still sends Origin on every
// cross-origin write, and that must match. A request with neither is not a
// browser's, and a cookie in it was put there on purpose.
func SameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
	default:
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	return true
}

// ChatCookie is the chat app's sign-in cookie.
const ChatCookie = "cafard_chat"

// ChatLife matches access's chat sign-in life: the cookie lasts as long as
// the sign-in can.
const ChatLife = 90 * 24 * time.Hour

// SetChatCookie signs the browser in to the chat app.
//
// SameSite=Lax rather than Strict: a phone that opens the app from a link,
// a QR code or the share sheet arrives on a top-level navigation from
// elsewhere, and Strict would send it there signed out. Writes are guarded
// by SameOrigin as well, so Lax costs nothing a cross-site page could use.
func SetChatCookie(w http.ResponseWriter, value string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: ChatCookie, Value: value, Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(ChatLife.Seconds())})
}

// ClearChatCookie signs the browser out.
func ClearChatCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: ChatCookie, Value: "", Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// ChatCookieValue is the request's chat sign-in cookie, or "".
func ChatCookieValue(r *http.Request) string {
	c, err := r.Cookie(ChatCookie)
	if err != nil {
		return ""
	}
	return c.Value
}
