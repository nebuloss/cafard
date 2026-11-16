package assets

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLayoutEmitsTheScriptOnlyWhenScripted(t *testing.T) {
	tpl, err := Templates(template.FuncMap{})
	if err != nil {
		t.Fatal(err)
	}
	for _, scripted := range []bool{false, true} {
		var out strings.Builder
		err := tpl.ExecuteTemplate(&out, "layout", Layout{Title: "Chats", Page: "home", Scripted: scripted})
		if err != nil {
			t.Fatal(err)
		}
		has := strings.Contains(out.String(), `src="/app/main.js"`)
		if has != scripted {
			t.Errorf("scripted=%v: script tag present=%v\n%s", scripted, has, out.String())
		}
		if !strings.Contains(out.String(), `data-page="home"`) {
			t.Errorf("no data-page in\n%s", out.String())
		}
	}
}

func TestLayoutEscapesTheTitle(t *testing.T) {
	tpl, err := Templates(template.FuncMap{})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := tpl.ExecuteTemplate(&out, "layout", Layout{Title: "<script>x</script>"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<script>x") {
		t.Errorf("title not escaped:\n%s", out.String())
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
