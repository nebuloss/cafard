package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A JSON read the browser has is 304; anything else passes untouched.
func TestETag(t *testing.T) {
	body := `{"members":[]}`
	h := ETag(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Write([]byte(body))
		case "/refused":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"no"}`))
		default:
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("a file"))
		}
	}))
	get := func(path, inm string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if inm != "" {
			r.Header.Set("If-None-Match", inm)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := get("/json", "")
	tag := first.Header().Get("ETag")
	if first.Code != 200 || first.Body.String() != body || tag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("first read: %d %q %v", first.Code, first.Body.String(), first.Header())
	}
	if again := get("/json", tag); again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Errorf("read again with its tag: %d %q", again.Code, again.Body.String())
	}
	if other := get("/json", `"something-else"`); other.Code != 200 || other.Body.String() != body {
		t.Errorf("another tag: %d", other.Code)
	}
	if w := get("/refused", ""); w.Code != 403 || w.Header().Get("ETag") != "" {
		t.Errorf("a refusal was tagged: %d %v", w.Code, w.Header())
	}
	if w := get("/file", ""); w.Body.String() != "a file" || w.Header().Get("ETag") != "" {
		t.Errorf("a file was tagged: %v", w.Header())
	}
}
