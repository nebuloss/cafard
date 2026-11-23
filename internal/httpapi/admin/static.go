package admin

import (
	"context"
	"net/http"

	"cafard/internal/httpapi/httpx"
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

// static serves the admin UI. A binary built without the UI (a bare go
// build) says how to get it.
func (a *Admin) static() http.Handler {
	return httpx.SPA(a.ui, uiPolicy,
		"cafard admin: this binary was built without its UI. Build it with `make build`, which runs `make web` first.\nThe admin API is at /admin/api.\n")
}
