package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
)

func newSessions(t *testing.T) (*Tokens, *Sessions, *Pairings, *clock.Fake) {
	t.Helper()
	tokens, c := newTokens(t)
	s := NewSessions(tokens.db, c)
	return tokens, s, NewPairings(tokens.db, c, s, nil), c
}

func TestSessionLifecycle(t *testing.T) {
	tokens, sessions, _, clk := newSessions(t)
	ctx := context.Background()
	if _, err := tokens.EnsurePrincipal(ctx, "guillaume", KindHuman); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sessions.Create(ctx, "guillaume", NewScopeSet(ScopeRead, ScopeAdmin), "", ""); !errors.Is(err, ErrAdminScope) {
		t.Errorf("a session with the admin scope: %v", err)
	}
	row, secret, err := sessions.Create(ctx, "guillaume", NewScopeSet(ScopeRead, ScopeIngest), "Firefox on Linux", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, SessionPrefix) {
		t.Errorf("secret %q has no prefix", secret)
	}
	var stored string
	if err := tokens.db.QueryRow(`SELECT hash FROM sessions WHERE id = ?`, row.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == secret || stored != HashToken(secret) {
		t.Error("the session is not stored hashed")
	}
	p, err := sessions.Lookup(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "guillaume" || p.Kind != KindHuman || p.SessionID != row.ID || p.TokenID != 0 || !p.Has(ScopeIngest) || p.Has(ScopeMessage) {
		t.Errorf("principal = %+v", p)
	}
	if _, err := sessions.Lookup(ctx, "cfs_nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown session: %v", err)
	}
	if _, err := sessions.Lookup(ctx, "cfd_token"); !errors.Is(err, ErrInvalid) {
		t.Errorf("token as cookie: %v", err)
	}

	// Used after 20 days, it is renewed; left alone 30 days, it expires.
	clk.Advance(20 * 24 * time.Hour)
	if _, err := sessions.Lookup(ctx, secret); err != nil {
		t.Fatalf("after 20 days: %v", err)
	}
	clk.Advance(29 * 24 * time.Hour)
	if _, err := sessions.Lookup(ctx, secret); err != nil {
		t.Fatalf("renewed session expired: %v", err)
	}
	clk.Advance(31 * 24 * time.Hour)
	if _, err := sessions.Lookup(ctx, secret); !errors.Is(err, ErrInvalid) {
		t.Errorf("expired session: %v", err)
	}
	if err := sessions.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Get(ctx, row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session kept: %v", err)
	}
}

func TestSessionRevokeDeletesTheRow(t *testing.T) {
	tokens, sessions, _, _ := newSessions(t)
	ctx := context.Background()
	tokens.EnsurePrincipal(ctx, "guillaume", KindHuman)
	row, secret, err := sessions.Create(ctx, "guillaume", NewScopeSet(ScopeRead), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Revoke(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Lookup(ctx, secret); !errors.Is(err, ErrInvalid) {
		t.Errorf("revoked session still works: %v", err)
	}
	if err := sessions.Revoke(ctx, row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second revoke: %v", err)
	}
}

func TestTheAuthenticatorReadsTheCookieAndBearerWins(t *testing.T) {
	tokens, sessions, _, _ := newSessions(t)
	ctx := context.Background()
	tokens.EnsurePrincipal(ctx, "guillaume", KindHuman)
	tokens.EnsurePrincipal(ctx, "codex", KindAgent)
	_, cookie, _ := sessions.Create(ctx, "guillaume", NewScopeSet(ScopeRead), "", "")
	_, bearer, _ := tokens.CreateToken(ctx, "codex", "", NewScopeSet(ScopeRead))
	a := &TokenAuthenticator{Tokens: tokens, Sessions: sessions.Lookup}

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(cookieOf(cookie))
	if p, err := a.FromRequest(r); err != nil || p.Name != "guillaume" {
		t.Errorf("cookie: %+v %v", p, err)
	}
	r.Header.Set("Authorization", "Bearer "+bearer)
	if p, err := a.FromRequest(r); err != nil || p.Name != "codex" {
		t.Errorf("bearer and cookie: %+v %v", p, err)
	}
	if _, err := (BearerOnly{Next: a}).FromRequest(withCookieOnly(cookie)); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("BearerOnly accepted a cookie: %v", err)
	}
}

func TestPairingIsSingleUseAndExpires(t *testing.T) {
	tokens, sessions, pairings, clk := newSessions(t)
	ctx := context.Background()
	tokens.EnsurePrincipal(ctx, "admin", KindHuman)

	row, code, err := pairings.Create(ctx, "", "guillaume-phone", nil, "", "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	tokens.db.QueryRow(`SELECT code_hash FROM pairings WHERE id = ?`, row.ID).Scan(&stored)
	if stored != HashToken(code) {
		t.Error("the code is not stored hashed")
	}
	if got, _, ok := pairings.Code(ctx, row.ID); !ok || got != code {
		t.Error("the pending code is not kept for the QR")
	}

	// Two phones scan at once: one wins.
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := pairings.Redeem(ctx, code, "Mozilla/5.0 (Linux; Android 15) Chrome/141.0 Mobile Safari/537.36"); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d redemptions succeeded", wins)
	}
	p, err := tokens.Principal(ctx, "guillaume-phone")
	if err != nil || p.Kind != KindHuman {
		t.Errorf("new human: %+v %v", p, err)
	}
	list, _ := sessions.List(ctx, "guillaume-phone")
	if len(list) != 1 || list[0].Scopes.String() != PairingScopes.String() || list[0].Label != "Chrome on Android (paired)" {
		t.Errorf("sessions = %+v", list)
	}
	if _, _, ok := pairings.Code(ctx, row.ID); ok {
		t.Error("a used code is still kept")
	}

	_, code2, err := pairings.Create(ctx, "admin", "", NewScopeSet(ScopeRead), "", "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, code3, err := pairings.Create(ctx, "admin", "", nil, "", "cli", 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(PairingTTL + time.Second)
	if _, _, err := pairings.Redeem(ctx, code2, ""); !errors.Is(err, ErrPairingUsed) {
		t.Errorf("expired code: %v", err)
	}
	if _, _, err := pairings.Redeem(ctx, code3, ""); err != nil {
		t.Errorf("a two-hour code after five minutes: %v", err)
	}
}

func TestPairingNeverGrantsAdminNorLastsPastADay(t *testing.T) {
	tokens, _, pairings, _ := newSessions(t)
	ctx := context.Background()
	tokens.EnsurePrincipal(ctx, "guillaume", KindHuman)
	if _, _, err := pairings.Create(ctx, "guillaume", "", NewScopeSet(ScopeRead, ScopeAdmin), "", "admin", 0); !errors.Is(err, ErrBadPairing) {
		t.Errorf("admin scope: %v", err)
	}
	if _, _, err := pairings.Create(ctx, "guillaume", "", nil, "", "cli", MaxPairingTTL+time.Minute); !errors.Is(err, ErrBadPairing) {
		t.Errorf("ttl over a day: %v", err)
	}
}

func TestPairingRefusesNonHumans(t *testing.T) {
	tokens, _, pairings, _ := newSessions(t)
	ctx := context.Background()
	tokens.EnsurePrincipal(ctx, "codex", KindAgent)
	if _, _, err := pairings.Create(ctx, "codex", "", nil, "", "admin", 0); !errors.Is(err, ErrConflict) {
		t.Errorf("agent: %v", err)
	}
	if _, _, err := pairings.Create(ctx, "", "codex", nil, "", "admin", 0); !errors.Is(err, ErrConflict) {
		t.Errorf("new name of an agent: %v", err)
	}
	if _, _, err := pairings.Create(ctx, "nobody", "", nil, "", "admin", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown principal: %v", err)
	}
	if _, _, err := pairings.Create(ctx, "a", "b", nil, "", "admin", 0); err == nil {
		t.Error("both set accepted")
	}
	if _, _, err := pairings.Create(ctx, "", "Bad Name", nil, "", "admin", 0); err == nil {
		t.Error("bad name accepted")
	}
}

func TestSessionLabel(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0":                                       "Firefox on Linux",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile Safari/604.1": "Safari on iOS",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/141.0 Safari/537.36 Edg/141.0":            "Edge on Windows",
		"": "Browser",
	} {
		if got := SessionLabel(ua); got != want {
			t.Errorf("%q: got %q, want %q", ua, got, want)
		}
	}
}

func cookieOf(secret string) *http.Cookie { return &http.Cookie{Name: SessionCookie, Value: secret} }

func withCookieOnly(secret string) *http.Request {
	r := httptest.NewRequest("GET", "/mcp", nil)
	r.AddCookie(cookieOf(secret))
	return r
}
