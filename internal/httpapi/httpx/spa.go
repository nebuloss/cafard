package httpx

import (
	"bytes"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"

	"cafard/internal/i18n"
)

// ThemeCookie holds the reader's choice of theme, "light" or "dark", set by
// the pages (web/src/shared/theme.ts); without it they follow the system.
const ThemeCookie = "cafard_theme"

// ThemeOf is the request's theme, "" to follow the system.
func ThemeOf(r *http.Request) string {
	if c, err := r.Cookie(ThemeCookie); err == nil && (c.Value == "light" || c.Value == "dark") {
		return c.Value
	}
	return ""
}

var htmlTag = regexp.MustCompile(`<html[^>]*>`)

// SPA serves a single-page UI from ui: its files as they are, and its page
// for every other path, which the page routes on its hash. policy is the
// Content-Security-Policy every answer carries. A nil ui is a binary built
// without the UI (a bare go build): every path then says so, in missing.
//
// The page is written with the reader's language and theme on its <html>,
// from their cookies: the policy allows no inline script to set them before
// the first paint, and without them a dark reader saw a light page flash.
func SPA(ui fs.FS, policy, missing string) http.Handler {
	if ui == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(missing))
		})
	}
	files := http.FileServerFS(ui)
	index, _ := fs.ReadFile(ui, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", policy)
		h.Set("Cache-Control", "no-cache")
		if p := r.URL.Path; p != "/" && p != "/index.html" {
			if f, err := ui.Open(strings.TrimPrefix(p, "/")); err == nil {
				f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		if index == nil {
			files.ServeHTTP(w, r)
			return
		}
		h.Set("Vary", "Cookie, Accept-Language")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(page(index, r)))
	})
}

// page is index with the request's language and theme on its <html>.
func page(index []byte, r *http.Request) []byte {
	tag := `<html lang="` + string(i18n.FromRequest(r)) + `"`
	if th := ThemeOf(r); th != "" {
		tag += ` data-theme="` + th + `"`
	}
	done := false
	return htmlTag.ReplaceAllFunc(index, func(m []byte) []byte {
		if done {
			return m
		}
		done = true
		return []byte(tag + ">")
	})
}
