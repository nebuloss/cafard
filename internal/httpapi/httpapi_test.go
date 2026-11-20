package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cafard/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Offline(m) }

func get(t *testing.T, h http.Handler, path string) (int, string, http.Header) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Set the raw path so the client sends it as written, dots included.
	req.URL.Opaque = path
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

// The DMZ can never reach an admin path, however it is spelt.
func TestPublicListenerAnswers404ToEveryAdminPath(t *testing.T) {
	s := New(Deps{})
	for _, p := range []string{"/admin", "/admin/", "/admin/api/projects", "/ADMIN/api", "/x/../admin/api", "/./admin"} {
		if code, _, _ := get(t, s.Public(), p); code != http.StatusNotFound {
			t.Errorf("public %s: %d, want 404", p, code)
		}
	}
}

func TestBothListenersAnswerHealthz(t *testing.T) {
	s := New(Deps{})
	for name, h := range map[string]http.Handler{"public": s.Public(), "admin": s.Admin()} {
		code, body, _ := get(t, h, "/healthz")
		if code != http.StatusOK || !strings.HasPrefix(body, "ok ") {
			t.Errorf("%s /healthz: %d %q", name, code, body)
		}
	}
}

// The two surfaces are separate handlers: what one serves at its root, the
// other does not.
func TestSurfacesAreSeparate(t *testing.T) {
	s := New(Deps{})
	_, pub, _ := get(t, s.Public(), "/")
	_, adm, _ := get(t, s.Admin(), "/")
	if pub == adm || !strings.Contains(adm, "admin") || strings.Contains(pub, "admin") {
		t.Errorf("public / = %q, admin / = %q", pub, adm)
	}
}

func TestEveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	s := New(Deps{})
	for name, h := range map[string]http.Handler{"public": s.Public(), "admin": s.Admin()} {
		for _, p := range []string{"/", "/healthz", "/nothing-here"} {
			_, _, hdr := get(t, h, p)
			if hdr.Get("X-Content-Type-Options") != "nosniff" || hdr.Get("X-Frame-Options") != "DENY" {
				t.Errorf("%s %s: headers %v", name, p, hdr)
			}
		}
	}
}

func TestCleanPath(t *testing.T) {
	for in, want := range map[string]string{
		"":                "/",
		"/":               "/",
		"/admin":          "/admin",
		"/a/./b/../admin": "/a/admin",
		"/../../admin/x":  "/admin/x",
		"admin":           "/admin",
		"//admin//api":    "/admin/api",
	} {
		if got := cleanPath(in); got != want {
			t.Errorf("cleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}
