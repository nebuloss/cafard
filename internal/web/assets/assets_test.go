package assets

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLayout(t *testing.T) {
	tpl, err := Templates(template.FuncMap{})
	if err != nil {
		t.Fatal(err)
	}
	const script = `src="/app/main.js"`
	for _, c := range []struct {
		name      string
		l         Layout
		want, not []string
	}{
		{"no script without the bundle", Layout{Title: "Chats", Page: "home"}, []string{`data-page="home"`}, []string{script}},
		{"the script with the bundle", Layout{Title: "Chats", Page: "home", Scripted: true}, []string{`data-page="home"`, script}, nil},
		{"the title is escaped", Layout{Title: "<script>x</script>"}, nil, []string{"<script>x"}},
	} {
		var out strings.Builder
		if err := tpl.ExecuteTemplate(&out, "layout", c.l); err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: no %s in\n%s", c.name, w, out.String())
			}
		}
		for _, n := range c.not {
			if strings.Contains(out.String(), n) {
				t.Errorf("%s: %s in\n%s", c.name, n, out.String())
			}
		}
	}
}

func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	if err := Register(mux); err != nil {
		t.Fatal(err)
	}
	return mux
}

func get(mux http.Handler, target string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestStaticFilesAreServedAndRevalidated(t *testing.T) {
	mux := newMux(t)
	rec := get(mux, "/static/app.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("content type = %q", ct)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	if again := get(mux, "/static/app.css", "If-None-Match", etag); again.Code != http.StatusNotModified {
		t.Errorf("revalidation status = %d, want 304", again.Code)
	}
}

func TestBundleDirectoryIsNotBrowsable(t *testing.T) {
	mux := newMux(t)
	for _, p := range []string{"/static/app/README.txt", "/static/app/main.js", "/static/nope.css"} {
		if rec := get(mux, p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", p, rec.Code)
		}
	}
}

func TestBundleRouteFollowsBundleBuilt(t *testing.T) {
	rec := get(newMux(t), "/app/main.js")
	want := http.StatusNotFound
	if BundleBuilt() {
		want = http.StatusOK
	}
	if rec.Code != want {
		t.Errorf("status = %d, want %d (bundleBuilt=%v)", rec.Code, want, BundleBuilt())
	}
}
