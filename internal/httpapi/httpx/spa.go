package httpx

import (
	"io/fs"
	"net/http"
)

// SPA serves a single-page UI from ui: its files as they are, and its page
// for every other path, which the page routes on its hash. policy is the
// Content-Security-Policy every answer carries. A nil ui is a binary built
// without the UI (a bare go build): every path then says so, in missing.
func SPA(ui fs.FS, policy, missing string) http.Handler {
	if ui == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(missing))
		})
	}
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", policy)
		h.Set("Cache-Control", "no-cache")
		if r.URL.Path != "/" {
			if f, err := ui.Open(r.URL.Path[1:]); err == nil {
				f.Close()
			} else {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}
