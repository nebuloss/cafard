// Package webui is the web UIs compiled into the binary: the admin UI and,
// from step 4c, the chat app, built from web/ by `make web` into dist/.
//
// Git holds only dist/.gitkeep, as claudication's webdist does: a bare
// `go build` embeds no UI and still serves the APIs, and the surfaces say
// the UI is missing rather than serve an empty page. `make build` builds the
// UIs first.
//
// Each UI is its own directory and is served on its own listener, so the
// admin UI's code is never reachable from the public one.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Admin is the admin UI's files, or nil when the binary was built without it.
func Admin() fs.FS { return sub("dist/admin") }

// Chat is the chat app's files, or nil when the binary was built without it.
func Chat() fs.FS { return sub("dist/chat") }

func sub(dir string) fs.FS {
	f, err := fs.Sub(dist, dir)
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(f, "index.html"); err != nil {
		return nil
	}
	return f
}
