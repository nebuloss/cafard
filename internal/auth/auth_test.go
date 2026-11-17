package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/store"
)

func newTokens(t *testing.T) (*Tokens, *clock.Fake) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := clock.NewFake(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC))
	return NewTokens(st.DB(), c), c
}

func TestScopes(t *testing.T) {
	s, err := ParseScopes("message, read admin read")
	if err != nil {
		t.Fatal(err)
	}
	if s.String() != "read message admin" {
		t.Errorf("scopes = %q", s.String())
	}
	if s.Has(ScopeIngest) || !s.Has(ScopeAdmin) {
		t.Error("Has is wrong")
	}
	if _, err := ParseScopes("read,root"); err == nil {
		t.Error("unknown scope accepted")
	}
}

func TestNames(t *testing.T) {
	for _, ok := range []string{"admin", "claude-laptop", "codex.build_2", "a"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Admin", "a~b", "@humans", "-x", strings.Repeat("a", 41), "a b"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTokenLifecycle(t *testing.T) {
	tokens, _ := newTokens(t)
	ctx := context.Background()
	if _, err := tokens.EnsurePrincipal(ctx, "codex-build", KindAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.EnsurePrincipal(ctx, "codex-build", KindHuman); !errors.Is(err, ErrConflict) {
		t.Errorf("kind change err = %v", err)
	}
	tr, secret, err := tokens.CreateToken(ctx, "codex-build", "ci", NewScopeSet(ScopeRead, ScopeMessage))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, TokenPrefix) || len(secret) < 40 {
		t.Errorf("secret = %q", secret)
	}
	var stored string
	if err := tokens.db.QueryRow(`SELECT hash FROM tokens WHERE id = ?`, tr.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == secret || stored != HashToken(secret) {
		t.Error("the database must hold the hash, not the token")
	}
	p, err := tokens.Lookup(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "codex-build" || p.Kind != KindAgent || !p.Has(ScopeMessage) || p.TokenID != tr.ID {
		t.Errorf("principal = %+v", p)
	}
	rows, _ := tokens.ListTokens(ctx, "")
	if len(rows) != 1 || rows[0].LastUsed == nil {
		t.Errorf("list = %+v", rows)
	}
	if err := tokens.RevokeToken(ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Lookup(ctx, secret); !errors.Is(err, ErrInvalid) {
		t.Errorf("revoked token err = %v", err)
	}
	if err := tokens.RevokeToken(ctx, tr.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second revoke err = %v", err)
	}
	if _, err := tokens.Lookup(ctx, "not-a-token"); !errors.Is(err, ErrInvalid) {
		t.Errorf("garbage err = %v", err)
	}
}

func TestTokensCannotCarryAdmin(t *testing.T) {
	tokens, _ := newTokens(t)
	ctx := context.Background()
	tokens.EnsurePrincipal(ctx, "guillaume", KindHuman)
	if _, _, err := tokens.CreateToken(ctx, "guillaume", "", NewScopeSet(ScopeRead, ScopeAdmin)); !errors.Is(err, ErrAdminScope) {
		t.Errorf("admin token: %v", err)
	}
}

func TestRequire(t *testing.T) {
	tokens, _ := newTokens(t)
	ctx := context.Background()
	tokens.EnsurePrincipal(ctx, "dev", KindDevice)
	_, ingest, _ := tokens.CreateToken(ctx, "dev", "", NewScopeSet(ScopeIngest))
	a := &TokenAuthenticator{Tokens: tokens}
	var seen Principal
	h := Require(a, ScopeIngest, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = PrincipalFrom(r.Context())
	}))
	do := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := do(""); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := do("cfd_wrong"); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "cfd_wrong") {
		t.Errorf("bad token: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(ingest); rec.Code != http.StatusOK || seen.Name != "dev" {
		t.Errorf("good token: %d %+v", rec.Code, seen)
	}
	hRead := Require(a, ScopeRead, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+ingest)
	rec := httptest.NewRecorder()
	hRead.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `\"read\" scope`) {
		t.Errorf("missing scope: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		site, origin string
		want         bool
	}{
		{"same-origin", "", true},
		{"none", "", true},
		{"cross-site", "", false},
		{"same-site", "", false},
		{"", "http://cafard.lan:8790", true},
		{"", "http://evil.example", false},
		{"", "null", false},
		{"", "", true},
	}
	h := RequireSameOrigin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "http://cafard.lan:8790/x", nil)
		if c.site != "" {
			req.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Code == http.StatusOK; got != c.want {
			t.Errorf("site=%q origin=%q: allowed=%v", c.site, c.origin, got)
		}
	}
	get := httptest.NewRequest(http.MethodGet, "http://cafard.lan/x", nil)
	get.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		t.Error("a cross-site GET is not a write and passes")
	}
}

func TestIsRoutableFailsClosed(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8790": false, "[::1]:8790": false, "localhost:8790": false,
		"0.0.0.0:8790": true, ":8790": true, "10.0.50.2:8790": true, "garbage": true,
		"no-such-host.invalid:1": true,
	}
	for addr, want := range cases {
		if got := IsRoutable(addr); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}

func TestExposureGuard(t *testing.T) {
	unset := func(context.Context) (bool, error) { return false, nil }
	set := func(context.Context) (bool, error) { return true, nil }
	ctx := context.Background()
	if err := ExposureGuard(ctx, unset, "127.0.0.1:1", ""); err != nil {
		t.Errorf("loopback: %v", err)
	}
	if err := ExposureGuard(ctx, unset, "127.0.0.1:1", "0.0.0.0:2"); err == nil || !strings.Contains(err.Error(), "0.0.0.0:2") {
		t.Errorf("routable admin interface with no password: %v", err)
	}
	if err := ExposureGuard(ctx, set, "0.0.0.0:1"); err != nil {
		t.Errorf("routable with a password: %v", err)
	}
}
