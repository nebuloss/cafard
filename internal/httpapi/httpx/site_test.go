package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestPublicURL(t *testing.T) {
	p := NewPublicURL("http://10.0.60.9:8790/")
	if p.String() != "http://10.0.60.9:8790" || p.IsSet() {
		t.Errorf("from the file: %s %v", p, p.IsSet())
	}
	p.Set("https://cafard.example/")
	if p.String() != "https://cafard.example" || !p.IsSet() || p.File() != "http://10.0.60.9:8790" {
		t.Errorf("set: %s %v", p, p.IsSet())
	}
	p.Set("")
	if p.String() != "http://10.0.60.9:8790" || p.IsSet() {
		t.Errorf("back to the file: %s", p)
	}
}

// A trusted proxy is believed about the client and the scheme; anyone else
// sending the same headers is not.
func TestProxied(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.60.2/32"), netip.MustParsePrefix("10.0.10.1/32")}
	var ip string
	var https bool
	h := Proxied(trusted, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, https = ClientIP(r), IsHTTPS(r)
	}))
	for _, c := range []struct {
		from, xff, proto, ip string
		https                bool
	}{
		{"10.0.60.2:4000", "203.0.113.7", "https", "203.0.113.7", true},
		{"10.0.60.2:4000", "198.51.100.1, 203.0.113.7", "http", "203.0.113.7", false},
		{"10.0.60.2:4000", "203.0.113.7, 10.0.10.1", "https", "203.0.113.7", true},
		{"10.0.60.2:4000", "", "", "10.0.60.2", false},
		{"10.0.60.2:4000", "not-an-ip", "", "10.0.60.2", false},
		{"192.0.2.5:4000", "203.0.113.7", "https", "192.0.2.5", false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.from
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		r.Header.Set("X-Forwarded-Proto", c.proto)
		h.ServeHTTP(httptest.NewRecorder(), r)
		if ip != c.ip || https != c.https {
			t.Errorf("from %s, XFF %q, proto %q: %s %v, want %s %v", c.from, c.xff, c.proto, ip, https, c.ip, c.https)
		}
	}
}
