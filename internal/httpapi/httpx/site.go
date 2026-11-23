package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
)

// PublicURL is where people and agents reach the public listener: what join
// links, upload links and the MCP config an agent's script writes point at.
//
// It changes while the box runs: the admin sets it in the admin UI's
// settings, over public-url in the file, and every link made after that
// carries the new one. Links already given keep the address they were given
// with, so changing it is for moving the box, not for every day.
type PublicURL struct {
	file string
	set  atomic.Pointer[string]
}

// NewPublicURL is the URL from the configuration file, until one is set.
func NewPublicURL(file string) *PublicURL {
	return &PublicURL{file: strings.TrimSuffix(file, "/")}
}

// String is the URL in effect, without a trailing slash.
func (p *PublicURL) String() string {
	if s := p.set.Load(); s != nil {
		return *s
	}
	return p.file
}

// File is the configuration file's URL, the one in effect when none is set.
func (p *PublicURL) File() string { return p.file }

// IsSet reports whether the admin set one over the file's.
func (p *PublicURL) IsSet() bool { return p.set.Load() != nil }

// Set puts u in effect; empty goes back to the file's.
func (p *PublicURL) Set(u string) {
	if u == "" {
		p.set.Store(nil)
		return
	}
	u = strings.TrimSuffix(u, "/")
	p.set.Store(&u)
}

type httpsKey struct{}

// IsHTTPS reports whether the browser reached cafard over https, directly or
// through a trusted proxy that says so: a cookie is marked Secure then, and
// only then. Deciding it from the public URL instead broke the admin UI
// opened on its plain internal address once the public URL was https.
func IsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	https, _ := r.Context().Value(httpsKey{}).(bool)
	return https
}

// Proxied believes the proxies in trusted about who is asking: for a
// request from one of them, the client's address becomes the nearest address
// in X-Forwarded-For that is not a trusted proxy, and X-Forwarded-Proto says
// whether the browser used https. Without it every client behind the proxy
// shares the proxy's address, and with it one rate-limit bucket: ten wrong
// sign-ins from anyone would lock everyone out for a minute.
//
// From anywhere else the headers are ignored, so a client cannot claim an
// address by sending them.
func Proxied(trusted []netip.Prefix, next http.Handler) http.Handler {
	if len(trusted) == 0 {
		return next
	}
	isTrusted := func(s string) bool {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			return false
		}
		a = a.Unmap()
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !isTrusted(host) {
			next.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			h := strings.TrimSpace(hops[i])
			if _, err := netip.ParseAddr(h); err != nil {
				break
			}
			if !isTrusted(h) {
				r2.RemoteAddr = net.JoinHostPort(h, "0")
				break
			}
		}
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			r2 = r2.WithContext(context.WithValue(r2.Context(), httpsKey{}, true))
		}
		next.ServeHTTP(w, r2)
	})
}
