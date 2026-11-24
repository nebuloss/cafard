package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// The page carries the reader's language and theme from the first paint;
// the other files are served as they are.
func TestSPAWritesLanguageAndTheme(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":   {Data: []byte("<!doctype html>\n<html lang=\"en\">\n<head></head><body></body></html>")},
		"assets/a.css": {Data: []byte("body{}")},
	}
	h := SPA(ui, "default-src 'self'", "missing")
	get := func(path string, cookies ...*http.Cookie) (int, string) {
		r := httptest.NewRequest("GET", path, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		b, _ := io.ReadAll(w.Result().Body)
		return w.Code, string(b)
	}
	if _, body := get("/"); !strings.Contains(body, `<html lang="en">`) {
		t.Errorf("no cookie:\n%s", body)
	}
	if _, body := get("/be98/smoke", &http.Cookie{Name: "cafard_lang", Value: "fr"}, &http.Cookie{Name: ThemeCookie, Value: "dark"}); !strings.Contains(body, `<html lang="fr" data-theme="dark">`) {
		t.Errorf("French, dark, on a deep path:\n%s", body)
	}
	if _, body := get("/", &http.Cookie{Name: ThemeCookie, Value: "neon"}); !strings.Contains(body, `<html lang="en">`) {
		t.Errorf("an unknown theme was written:\n%s", body)
	}
	if code, body := get("/assets/a.css"); code != 200 || body != "body{}" {
		t.Errorf("a file: %d %q", code, body)
	}
}
