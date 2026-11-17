package auth

import (
	"net/http"
	"time"
)

// cookieMaxAge is how long the browser keeps the cookie. The server decides
// when a session ends (SessionTTL, renewed while used), so this is the
// longest browsers allow.
const cookieMaxAge = 400 * 24 * time.Hour

// SetSessionCookie gives the browser its session. HttpOnly: no script ever
// needs the secret. Lax: a link from elsewhere still lands signed in, and
// cross-site writes are refused by RequireSameOrigin anyway.
func SetSessionCookie(w http.ResponseWriter, secret string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    secret,
		Path:     "/",
		MaxAge:   int(cookieMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie removes it.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// SetAdminCookie gives the browser its admin session. Strict: no link from
// another site may arrive signed in as the admin. Its lifetime is the
// session's, which is not renewed.
func SetAdminCookie(w http.ResponseWriter, secret string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminCookie,
		Value:    secret,
		Path:     "/",
		MaxAge:   int(AdminSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearAdminCookie removes it.
func ClearAdminCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}
