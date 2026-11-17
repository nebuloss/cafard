package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/nebuloss/cafard/internal/dto"
)

// Authenticator establishes who made a request.
type Authenticator interface {
	// FromRequest reads "Authorization: Bearer cfd_..." or the cookies
	// (CookiePrincipal). Bearer wins when both are present. It returns
	// ErrNoCredentials or ErrInvalid.
	FromRequest(r *http.Request) (Principal, error)
}

// BearerOnly wraps an Authenticator and refuses anything but a bearer token.
// /mcp uses it: a cookie must never authenticate an MCP call.
type BearerOnly struct{ Next Authenticator }

func (b BearerOnly) FromRequest(r *http.Request) (Principal, error) {
	if _, ok := BearerToken(r); !ok {
		return Principal{}, ErrNoCredentials
	}
	p, err := b.Next.FromRequest(r)
	if err != nil {
		return Principal{}, err
	}
	if p.TokenID == 0 {
		return Principal{}, ErrInvalid
	}
	return p, nil
}

// SessionLookup resolves a session cookie value (Sessions.Lookup).
type SessionLookup func(ctx context.Context, cookie string) (Principal, error)

// SessionCookie is the name of the browser session cookie.
const SessionCookie = "cafard_session"

// TokenAuthenticator checks bearer tokens, then the cookies: the admin
// session when Admin is set and the request came through the admin
// interface, then the browser session if a session lookup is set.
type TokenAuthenticator struct {
	Tokens   *Tokens
	Sessions SessionLookup
	Admin    *Admin
}

func (a *TokenAuthenticator) FromRequest(r *http.Request) (Principal, error) {
	if token, ok := BearerToken(r); ok {
		return a.Tokens.Lookup(r.Context(), token)
	}
	return CookiePrincipal(r, a.Admin, a.Sessions)
}

// CookiePrincipal is the principal of the request's cookies. A stale admin
// cookie is skipped rather than refused, so a browser that also holds a
// session keeps it after a restart.
func CookiePrincipal(r *http.Request, admin *Admin, sessions SessionLookup) (Principal, error) {
	if admin != nil && OnAdminInterface(r.Context()) {
		if c, err := r.Cookie(AdminCookie); err == nil && c.Value != "" {
			if p, err := admin.Lookup(c.Value); err == nil {
				return p, nil
			}
		}
	}
	if sessions != nil {
		if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
			return sessions(r.Context(), c.Value)
		}
	}
	return Principal{}, ErrNoCredentials
}

type adminInterfaceKey struct{}

// AdminInterface marks the requests next serves as coming through the
// admin interface, the only place an admin cookie counts.
func AdminInterface(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminInterfaceKey{}, true)))
	})
}

// OnAdminInterface reports whether AdminInterface marked the request.
func OnAdminInterface(ctx context.Context) bool {
	on, _ := ctx.Value(adminInterfaceKey{}).(bool)
	return on
}

// BearerToken extracts the token of an RFC 6750 Authorization header.
func BearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// Authenticate runs a and maps its failure to an API error.
func Authenticate(a Authenticator, r *http.Request) (Principal, *dto.APIError) {
	p, err := a.FromRequest(r)
	switch {
	case err == nil:
		return p, nil
	case errors.Is(err, ErrNoCredentials):
		return Principal{}, dto.Errorf(dto.CodeUnauthenticated, "This needs a token: send \"Authorization: Bearer cfd_...\", or sign in.")
	case errors.Is(err, ErrInvalid):
		return Principal{}, dto.Errorf(dto.CodeUnauthenticated, "This token or session is not valid. It may have been revoked.")
	default:
		return Principal{}, dto.Errorf("internal", "Checking credentials failed.")
	}
}

// MissingScope is the error for a principal without scope.
func MissingScope(scope Scope) *dto.APIError {
	return dto.Errorf(dto.CodeForbidden, "This needs the %q scope, which your token or session does not have.", string(scope))
}

// Require authenticates the request, checks the scope, and puts the
// principal in the context. An empty scope only authenticates.
func Require(a Authenticator, scope Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, apiErr := Authenticate(a, r)
		if apiErr != nil {
			if apiErr.Code == dto.CodeUnauthenticated {
				w.Header().Set("WWW-Authenticate", `Bearer realm="cafard"`)
			}
			dto.WriteError(w, apiErr)
			return
		}
		if scope != "" && !p.Has(scope) {
			dto.WriteError(w, MissingScope(scope))
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// RequireSameOrigin refuses cross-origin writes. It trusts Sec-Fetch-Site,
// which browsers set and pages cannot forge, and falls back to comparing
// Origin with Host. A request with neither header is not from a browser
// and passes; bearer-only routes do not need this check at all.
func RequireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) && !SameOrigin(r) {
			dto.WriteError(w, dto.Errorf(dto.CodeCrossOrigin, "This request came from another site, so it was refused."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// SameOrigin reports whether a request comes from this site, or from no
// browser at all.
func SameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "same-site", "cross-site":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// IsRoutable reports whether an address could be reached from another
// machine. It fails closed: anything that cannot be parsed or resolved
// counts as routable.
func IsRoutable(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback()
	}
	if host == "localhost" {
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return true
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return true
		}
	}
	return false
}

// ExposureGuard refuses to listen on a routable address while no admin
// password is set: nobody could then revoke a leaked token or pair a phone.
// Empty addresses are skipped.
func ExposureGuard(ctx context.Context, hasPassword func(context.Context) (bool, error), addresses ...string) error {
	exposed := ""
	for _, a := range addresses {
		if a != "" && IsRoutable(a) {
			exposed = a
			break
		}
	}
	if exposed == "" {
		return nil
	}
	ok, err := hasPassword(ctx)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("refusing to listen on %s: no admin password is set.\n"+
		"Set one with: cafard passwd\n"+
		"or listen on loopback (-http-listen 127.0.0.1:8790)", exposed)
}
