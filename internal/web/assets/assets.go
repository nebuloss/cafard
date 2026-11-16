// Package assets embeds the templates and static files, and serves the
// static ones.
package assets

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed templates static
var files embed.FS

const bundlePath = "static/app/main.js"

// bundleBuilt reports whether the TypeScript bundle was present at compile
// time, so the layout skips a script tag that would only 404.
var bundleBuilt = func() bool {
	info, err := fs.Stat(files, bundlePath)
	return err == nil && info.Size() > 0
}()

// BundleBuilt reports whether this binary carries the TypeScript bundle.
func BundleBuilt() bool { return bundleBuilt }

// Layout is the data the shell template needs on every page. Every field
// but the first three may be left zero: the sign-in page has no user, no
// navigation and no twins.
type Layout struct {
	Title    string
	Page     string // <body data-page>, picks the TypeScript Page
	Scripted bool   // emit the bundle's script tag

	Theme  string // "system" | "light" | "dark", from the theme cookie
	Nav    string // the active navigation item: chats, live, streams, agents, pair
	User   *User  // nil when signed out
	Badges Badges
	// Twins is the rendered <template id="tpl-..."> elements the page lists
	// (CONTRACTS.md 6.2), placed at the end of <body>.
	Twins template.HTML
	// Notice and Error are a snackbar and a banner the server shows once.
	Notice, Error string
	// Back is where the top bar's back arrow goes, or "".
	Back string
	// Path is the current path and query, where the theme switch returns.
	Path string
}

// Toast is one snackbar (partials/toast.html).
type Toast struct {
	Tone string // "info" | "error"
	Text string
}

// NoticeToast is the Notice as a toast.
func (l Layout) NoticeToast() Toast { return Toast{Tone: "info", Text: l.Notice} }

// User is the signed-in principal as the shell shows it.
type User struct {
	Name    string
	Kind    string
	Admin   bool
	Initial string
}

// Badges are the counts on the navigation items.
type Badges struct {
	Chats       int  // open questions for you + unread direct messages
	SourcesDown bool // error dot on Sources
}

// NewLayout fills in what does not depend on the page.
func NewLayout(title, page string) Layout {
	return Layout{Title: title, Page: page, Scripted: bundleBuilt}
}

// Templates parses every .html under templates/. Each file is named by its
// path relative to templates/ ("layout.html", "pages/home.html"), so
// partials in different directories cannot collide.
func Templates(funcs template.FuncMap) (*template.Template, error) {
	root := template.New("").Funcs(funcs)
	err := fs.WalkDir(files, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".html" {
			return err
		}
		body, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = root.New(strings.TrimPrefix(p, "templates/")).Parse(string(body))
		return err
	})
	if err != nil {
		return nil, err
	}
	return root, nil
}

type asset struct {
	body  []byte
	etag  string
	ctype string
}

// Register serves /static/* and /app/main.js from the embedded files.
func Register(mux *http.ServeMux) error {
	byPath := map[string]asset{}
	err := fs.WalkDir(files, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		ctype := mime.TypeByExtension(path.Ext(p))
		if path.Ext(p) == ".webmanifest" {
			ctype = "application/manifest+json"
		}
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		byPath[p] = asset{body: body, etag: `"` + hex.EncodeToString(sum[:12]) + `"`, ctype: ctype}
		return nil
	})
	if err != nil {
		return err
	}

	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/static/")
		if strings.HasPrefix(rel, "app/") {
			http.NotFound(w, r)
			return
		}
		serve(w, r, byPath, "static/"+rel)
	})
	mux.HandleFunc("GET /app/main.js", func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, byPath, bundlePath)
	})
	// Served at the root: a manifest's scope and a service worker's scope
	// are bounded by their URL. A file that is not there yet is a 404.
	for _, name := range rootFiles {
		mux.HandleFunc("GET /"+name, func(w http.ResponseWriter, r *http.Request) {
			serve(w, r, byPath, "static/"+name)
		})
	}
	return nil
}

// rootFiles are static files also served at "/<name>" (CONTRACTS.md 2.2).
var rootFiles = []string{"manifest.webmanifest", "sw.js", "icons.svg"}

func serve(w http.ResponseWriter, r *http.Request, byPath map[string]asset, p string) {
	a, ok := byPath[p]
	if !ok || len(a.body) == 0 {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("ETag", a.etag)
	// The bundle has a fixed name, so every load revalidates; the ETag makes
	// that a 304.
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, path.Base(p), time.Time{}, bytes.NewReader(a.body))
}
