package admin

import (
	"embed"
	"io/fs"
)

// The admin UI is plain HTML, CSS and JavaScript, with claudication's MD3
// tokens as CSS variables, and no build step: the files under ui/ are the
// files served. It is a small set of forms and lists on a network that may
// have no internet, and the bytes in the binary are the bytes in git.
//
//go:embed ui
var uiFiles embed.FS

// UI is the admin UI's files, rooted at ui/.
func UI() fs.FS {
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err)
	}
	return sub
}
