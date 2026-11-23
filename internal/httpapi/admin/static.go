package admin

import (
	"context"
	"net/http"

	"cafard/internal/store"
)

type signinKey struct{}

func withSignin(ctx context.Context, si store.Signin) context.Context {
	return context.WithValue(ctx, signinKey{}, si)
}

// signinFrom is the sign-in the request was made with.
func signinFrom(ctx context.Context) (store.Signin, bool) {
	si, ok := ctx.Value(signinKey{}).(store.Signin)
	return si, ok
}

// uiPolicy is the admin UI's Content-Security-Policy: its own files only, and
// data: images for the QR codes the API returns. No inline script and no
// other host: the admin listener is on a network that may have no internet
// at all, and nothing it shows should come from anywhere else.
const uiPolicy = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; " +
	"form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// static serves the admin UI's files. It is one page: every path that is not
// a file is that page, which routes on its hash.
func (a *Admin) static() http.Handler {
	files := http.FileServerFS(a.ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", uiPolicy)
		h.Set("Cache-Control", "no-cache")
		if r.URL.Path != "/" {
			if f, err := a.ui.Open(r.URL.Path[1:]); err == nil {
				f.Close()
			} else {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}
