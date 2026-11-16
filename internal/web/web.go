// Package web mounts the browser side of cafard: the pages, the JSON API
// and the SSE feed (track C), with the sessions and pairings they share.
package web

import (
	"context"
	"net/http"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/deps"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/api"
	"github.com/nebuloss/cafard/internal/web/pages"
	"github.com/nebuloss/cafard/internal/web/sse"
	"github.com/nebuloss/cafard/internal/web/view"
)

// Register mounts pages, /api/* (but /api/log and the message routes of
// track A) and /api/events. It has no background work of its own; expired
// sessions and pairings are removed by the janitor.
func Register(mux *http.ServeMux, _ *mcpserver.Server, d deps.Deps) (func(context.Context) error, error) {
	sessions := auth.NewSessions(d.Store.DB(), d.Clock)
	// One Pairings per process: it keeps the clear codes of pending QRs.
	pairings := auth.NewPairings(d.Store.DB(), d.Clock, sessions)
	kinds := view.NewKinds(d.Tokens)
	site, err := pages.New(d, sessions, pairings, kinds)
	if err != nil {
		return nil, err
	}
	site.Mount(mux)
	api.New(d, sessions, pairings, kinds).Mount(mux)
	sse.Register(mux, sse.New(d, kinds))
	if d.Janitor != nil {
		d.Janitor.Add(store.Task{Name: "sessions", Run: sessions.Expire})
	}
	return nil, nil
}
