// Package api is the JSON API under /api/ (CONTRACTS.md 2.3 and 3). Every
// route takes a bearer token or a session; writes with a session must be
// same-origin. Track C's routes are in the c_*.go files; track A adds the
// message routes in files of its own and one line in Mount.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/web/view"
)

// API holds what the handlers need.
type API struct {
	d        deps.Deps
	sessions *auth.Sessions
	pairings *auth.Pairings
	kinds    *view.Kinds
	log      *slog.Logger
}

// New builds the API.
func New(d deps.Deps, sessions *auth.Sessions, pairings *auth.Pairings, kinds *view.Kinds) *API {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &API{d: d, sessions: sessions, pairings: pairings, kinds: kinds, log: log}
}

// Mount registers the routes.
func (a *API) Mount(mux *http.ServeMux) {
	a.mountC(mux)
}

// Handler is an API handler with the caller's principal.
type Handler func(w http.ResponseWriter, r *http.Request, p auth.Principal)

// Read is a safe route needing scope.
func (a *API) Read(scope auth.Scope, h Handler) http.Handler {
	return auth.Require(a.d.Auth, scope, a.with(h))
}

// Write is a route that changes something: same-origin when it comes from
// a browser, then scope.
func (a *API) Write(scope auth.Scope, h Handler) http.Handler {
	return auth.RequireSameOrigin(auth.Require(a.d.Auth, scope, a.with(h)))
}

func (a *API) with(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.PrincipalFrom(r.Context())
		w.Header().Set("Cache-Control", "no-store")
		h(w, r, p)
	}
}

// maxBody bounds a JSON request body.
const maxBody = 64 << 10

// Decode reads a JSON body into v, answering the error itself when it
// cannot.
func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v)
	var tooBig *http.MaxBytesError
	switch {
	case err == nil:
		return true
	case errors.As(err, &tooBig):
		dto.WriteError(w, dto.Errorf(dto.CodeTooLarge, "The request body is larger than %d KiB.", maxBody>>10))
	case errors.Is(err, io.EOF):
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "The request needs a JSON body."))
	default:
		dto.WriteError(w, dto.Errorf(dto.CodeBadRequest, "The request body is not valid JSON: %v.", err))
	}
	return false
}

// Fail answers an error: an *APIError as is, anything else as a logged 500.
func (a *API) Fail(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *dto.APIError
	if errors.As(err, &apiErr) {
		dto.WriteError(w, apiErr)
		return
	}
	if r.Context().Err() == nil {
		a.log.Error("api request failed", "path", r.URL.Path, "err", err)
	}
	dto.WriteError(w, nil)
}
