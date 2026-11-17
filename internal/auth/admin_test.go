package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/clock"
)

func newAdmin(t *testing.T) (*Admin, *Tokens, *clock.Fake) {
	t.Helper()
	tokens, c := newTokens(t)
	return NewAdmin(tokens.db, c), tokens, c
}

func TestAdminPasswordSignsInAndRestartSignsOut(t *testing.T) {
	admin, tokens, clk := newAdmin(t)
	ctx := context.Background()
	if ok, _ := admin.HasPassword(ctx); ok {
		t.Fatal("a new database has a password")
	}
	if _, err := admin.SignIn(ctx, "10.0.0.1", "anything-at-all"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("sign in with no password set: %v", err)
	}
	if err := admin.SetPassword(ctx, "short"); !errors.Is(err, ErrShortPassword) {
		t.Errorf("short password: %v", err)
	}
	if err := admin.SetPassword(ctx, "correct horse"); err != nil {
		t.Fatal(err)
	}
	if p, err := tokens.Principal(ctx, AdminName); err != nil || p.Kind != KindHuman {
		t.Errorf("the admin principal: %+v %v", p, err)
	}
	var stored string
	if err := tokens.db.QueryRow(`SELECT password_hash FROM admin`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if parts := strings.Split(stored, "$"); len(parts) != 4 || parts[0] != "scrypt" || parts[1] != "32768" ||
		len(parts[2]) != 32 || len(parts[3]) != 64 || strings.Contains(stored, "correct horse") {
		t.Errorf("stored hash %q", stored)
	}

	if _, err := admin.SignIn(ctx, "10.0.0.1", "wrong horse"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: %v", err)
	}
	secret, err := admin.SignIn(ctx, "10.0.0.1", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := admin.Lookup(secret)
	if err != nil || p.Name != AdminName || p.Kind != KindHuman || p.Scopes.String() != "ingest read message admin" ||
		p.TokenID != 0 || p.SessionID != 0 {
		t.Errorf("admin session = %+v %v", p, err)
	}
	if _, err := admin.Lookup("cfs_" + secret[4:]); !errors.Is(err, ErrInvalid) {
		t.Errorf("a browser session prefix: %v", err)
	}

	// A restart is a new Admin over the same database.
	again := NewAdmin(tokens.db, clk)
	if _, err := again.Lookup(secret); !errors.Is(err, ErrInvalid) {
		t.Errorf("a session survived a restart: %v", err)
	}

	clk.Advance(AdminSessionTTL - time.Second)
	if _, err := admin.Lookup(secret); err != nil {
		t.Errorf("before 12 h: %v", err)
	}
	clk.Advance(time.Second)
	if _, err := admin.Lookup(secret); !errors.Is(err, ErrInvalid) {
		t.Errorf("after 12 h: %v", err)
	}
	other, _ := admin.NewSession()
	admin.SignOut(other)
	if _, err := admin.Lookup(other); !errors.Is(err, ErrInvalid) {
		t.Errorf("signed out: %v", err)
	}
}

func TestChangingThePasswordEndsAdminSessions(t *testing.T) {
	admin, _, _ := newAdmin(t)
	ctx := context.Background()
	if err := admin.SetPassword(ctx, "first password"); err != nil {
		t.Fatal(err)
	}
	secret, _ := admin.NewSession()
	if err := admin.ChangePassword(ctx, "not the password", "second password"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong current password: %v", err)
	}
	if _, err := admin.Lookup(secret); err != nil {
		t.Errorf("a refused change ended the session: %v", err)
	}
	if err := admin.ChangePassword(ctx, "first password", "second password"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Lookup(secret); !errors.Is(err, ErrInvalid) {
		t.Errorf("the session outlived the password: %v", err)
	}
	if _, err := admin.SignIn(ctx, "10.0.0.1", "second password"); err != nil {
		t.Errorf("new password: %v", err)
	}
}

func TestAdminSignInIsRateLimitedPerAddress(t *testing.T) {
	admin, _, clk := newAdmin(t)
	ctx := context.Background()
	// No password set: every attempt fails, but it still costs a key
	// derivation, so the limit is what stops a guesser.
	for i := range 10 {
		if _, err := admin.SignIn(ctx, "10.0.0.9", "guess"); !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := admin.SignIn(ctx, "10.0.0.9", "guess"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("eleventh attempt: %v", err)
	}
	if _, err := admin.SignIn(ctx, "10.0.0.10", "guess"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("another address: %v", err)
	}
	clk.Advance(6 * time.Second)
	if _, err := admin.SignIn(ctx, "10.0.0.9", "guess"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("after a refill: %v", err)
	}
}

func TestEnsurePasswordOnlyWhenNoneIsSet(t *testing.T) {
	admin, _, _ := newAdmin(t)
	ctx := context.Background()
	pw, err := admin.EnsurePassword(ctx)
	if err != nil || len(pw) != 36 {
		t.Fatalf("first = %q %v", pw, err)
	}
	if again, err := admin.EnsurePassword(ctx); err != nil || again != "" {
		t.Errorf("second = %q %v", again, err)
	}
}

// The admin cookie counts only on the admin interface; elsewhere the
// browser session, if any, is what authenticates.
func TestTheAdminCookieCountsOnlyOnTheAdminInterface(t *testing.T) {
	admin, tokens, clk := newAdmin(t)
	ctx := context.Background()
	sessions := NewSessions(tokens.db, clk)
	tokens.EnsurePrincipal(ctx, "guillaume", KindHuman)
	_, browser, _ := sessions.Create(ctx, "guillaume", NewScopeSet(ScopeRead), "", "")
	secret, _ := admin.NewSession()
	a := &TokenAuthenticator{Tokens: tokens, Sessions: sessions.Lookup, Admin: admin}

	var seen Principal
	var seenErr error
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen, seenErr = a.FromRequest(r) })
	req := func(cookies ...*http.Cookie) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}
	adminCookie := &http.Cookie{Name: AdminCookie, Value: secret}

	h.ServeHTTP(httptest.NewRecorder(), req(adminCookie))
	if !errors.Is(seenErr, ErrNoCredentials) {
		t.Errorf("admin cookie on the public interface: %+v %v", seen, seenErr)
	}
	AdminInterface(h).ServeHTTP(httptest.NewRecorder(), req(adminCookie))
	if seenErr != nil || !seen.Has(ScopeAdmin) {
		t.Errorf("admin cookie on the admin interface: %+v %v", seen, seenErr)
	}
	h.ServeHTTP(httptest.NewRecorder(), req(adminCookie, cookieOf(browser)))
	if seenErr != nil || seen.Name != "guillaume" || seen.Has(ScopeAdmin) {
		t.Errorf("both cookies on the public interface: %+v %v", seen, seenErr)
	}
	admin.SignOut(secret)
	AdminInterface(h).ServeHTTP(httptest.NewRecorder(), req(adminCookie, cookieOf(browser)))
	if seenErr != nil || seen.Name != "guillaume" {
		t.Errorf("a stale admin cookie hid the browser session: %+v %v", seen, seenErr)
	}
}
