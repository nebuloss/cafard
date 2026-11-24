package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromRequest(t *testing.T) {
	for _, c := range []struct {
		name, cookie, accept string
		want                 Lang
	}{
		{"nothing, as curl and MCP clients send", "", "", EN},
		{"French browser", "", "fr-FR,fr;q=0.9,en;q=0.8", FR},
		{"English browser", "", "en-US,en;q=0.9,fr;q=0.8", EN},
		{"the cookie beats the browser", "fr", "en-US,en;q=0.9", FR},
		{"and the other way", "en", "fr-FR,fr;q=0.9", EN},
		{"a cookie cafard does not know is ignored", "de", "fr", FR},
		{"q-values, not order", "", "en;q=0.5, fr;q=0.8", FR},
		{"the first language cafard has", "", "de-DE, fr;q=0.7, en;q=0.6", FR},
		{"q=0 is not this one", "", "fr;q=0, en;q=0.1", EN},
		{"a region alone", "", "fr-CA", FR},
		{"unknown languages only", "", "de, it;q=0.9", EN},
		{"a wildcard", "", "*", EN},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if c.cookie != "" {
			r.AddCookie(&http.Cookie{Name: Cookie, Value: c.cookie})
		}
		if c.accept != "" {
			r.Header.Set("Accept-Language", c.accept)
		}
		if got := FromRequest(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestT(t *testing.T) {
	if got, want := T(FR, "No file %d.", 7), "Aucun fichier 7."; got != want {
		t.Errorf("French: %q, want %q", got, want)
	}
	if got, want := T(EN, "No file %d.", 7), "No file 7."; got != want {
		t.Errorf("English: %q, want %q", got, want)
	}
	// With no French entry, French is the English.
	if got, want := T(FR, "cafard has no French for %s", "this"), "cafard has no French for this"; got != want {
		t.Errorf("no entry: %q, want %q", got, want)
	}
	if got, want := T(FR, "Uploads stop at 100%%."), "Les envois s'arrêtent à 100\u00a0%."; got != want {
		t.Errorf("a literal %%: %q, want %q", got, want)
	}
}
