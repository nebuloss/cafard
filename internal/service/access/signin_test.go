package access

import (
	"strings"
	"sync"
	"testing"
	"time"

	"cafard/internal/store"
)

func TestPasswordHashing(t *testing.T) {
	h, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash %q", h)
	}
	if !checkPassword(h, "correct horse battery") || checkPassword(h, "correct horse batterY") {
		t.Error("checkPassword")
	}
	if again, _ := hashPassword("correct horse battery"); again == h {
		t.Error("two hashes of one password are equal: no salt")
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$a$b", "$argon2id$v=19$x$a$b"} {
		if checkPassword(bad, "x") {
			t.Errorf("checkPassword accepted %q", bad)
		}
	}
}

// signIn is what pressing Sign in on a person's link does: it returns the
// sign-in's cookie.
func (w *world) signIn(person Who) string {
	w.t.Helper()
	code, _, err := w.svc.IssueLink(ctx, w.admin, person.Identity, 0)
	if err != nil {
		w.t.Fatal(err)
	}
	_, cookie, err := w.svc.SignInWithLink(ctx, code, "10.0.50.112", "Mozilla/5.0 (iPhone) Safari/605")
	if err != nil {
		w.t.Fatal(err)
	}
	return cookie
}

// People have no passwords: a link signs in the browser that uses it, once.
func TestASignInLinkSignsIn(t *testing.T) {
	w := newWorld(t)
	code, l, err := w.svc.IssueLink(ctx, w.admin, w.camille.Identity, time.Hour)
	if err != nil || l.Kind != store.PersonLink {
		t.Fatalf("link: %+v %v", l, err)
	}
	id, cookie, err := w.svc.SignInWithLink(ctx, code, "10.0.50.44", "Mozilla/5.0 (iPhone) Safari/605")
	if err != nil || id.Name != "camille" || cookie == "" {
		t.Fatalf("sign in: %v", err)
	}
	if _, _, err := w.svc.SignInWithLink(ctx, code, "x", ""); !Is(err, Spent) {
		t.Errorf("the link used twice: %v", err)
	}
	if got, _, err := w.svc.Session(ctx, ChatSurface, cookie, "10.0.50.44"); err != nil || got.Name != "camille" {
		t.Errorf("session: %+v %v", got, err)
	}
	// A chat sign-in is not an admin sign-in.
	if _, _, err := w.svc.Session(ctx, AdminSurface, cookie, "x"); !Is(err, Unauthorized) {
		t.Errorf("a chat cookie on the admin surface: %v", err)
	}
	// A second link signs in a second browser; the first stays signed in.
	second := w.signIn(w.camille)
	for _, c := range []string{cookie, second} {
		if _, _, err := w.svc.Session(ctx, ChatSurface, c, "x"); err != nil {
			t.Errorf("a sign-in ended by another: %v", err)
		}
	}
	if ss, _ := w.svc.Signins(ctx, w.camille); len(ss) != 2 {
		t.Errorf("%d sign-ins, want 2", len(ss))
	}
}

// A new link cancels the one before: one way in at a time.
func TestANewLinkCancelsTheOld(t *testing.T) {
	w := newWorld(t)
	old, _, _ := w.svc.IssueLink(ctx, w.admin, w.camille.Identity, 0)
	current, _, _ := w.svc.IssueLink(ctx, w.admin, w.camille.Identity, 0)
	if _, _, err := w.svc.SignInWithLink(ctx, old, "x", ""); !Is(err, Spent) {
		t.Errorf("the old link: %v", err)
	}
	if _, _, err := w.svc.SignInWithLink(ctx, current, "x", ""); err != nil {
		t.Errorf("the current link: %v", err)
	}
}

func TestLinksThatMustNotWork(t *testing.T) {
	w := newWorld(t)
	_, _, err := w.svc.IssueLink(ctx, w.devCode, w.camille.Identity, 0)
	wantCode(t, "an agent makes a link", err, NotAllowed)
	_, _, err = w.svc.IssueLink(ctx, w.camille, w.labBench.Identity, 0)
	wantCode(t, "a non-admin makes a link", err, NotAdmin)
	_, _, err = w.svc.IssueLink(ctx, w.admin, w.devBuild.Identity, 0)
	wantCode(t, "a link for a device", err, NotAllowed)
	_, _, err = w.svc.IssueLink(ctx, w.admin, w.camille.Identity, 8*24*time.Hour)
	wantCode(t, "a link for 8 days", err, BadValue)

	code, l, _ := w.svc.IssueLink(ctx, w.admin, w.camille.Identity, 0)
	if err := w.svc.CancelLink(ctx, w.admin, l); err != nil {
		t.Fatal(err)
	}
	_, _, err = w.svc.SignInWithLink(ctx, code, "x", "")
	wantCode(t, "a cancelled link", err, Spent)
	_, _, err = w.svc.SignInWithLink(ctx, "nonsense", "x", "")
	wantCode(t, "a made-up code", err, Spent)

	agentCode, _, _ := w.svc.IssueLink(ctx, w.admin, w.devCode.Identity, 0)
	_, _, err = w.svc.SignInWithLink(ctx, agentCode, "x", "")
	wantCode(t, "an agent's link used to sign in", err, NotAllowed)
	personCode, _, _ := w.svc.IssueLink(ctx, w.admin, w.camille.Identity, 0)
	_, _, err = w.svc.RedeemAgentLink(ctx, personCode, "x")
	wantCode(t, "a person's link used for a token", err, NotAllowed)
}

func TestAgentLinkGivesItsTokenOnce(t *testing.T) {
	w := newWorld(t)
	before, _ := w.svc.IssueToken(ctx, w.admin, w.devCode.Identity)
	code, _, _ := w.svc.IssueLink(ctx, w.admin, w.devCode.Identity, 0)
	id, token, err := w.svc.RedeemAgentLink(ctx, code, "10.0.50.20")
	if err != nil || id.Name != "dev-code" || !strings.HasPrefix(token, "cfd_ag_") {
		t.Fatalf("redeem: %v", err)
	}
	if got, err := w.svc.Authenticate(ctx, token, "10.0.50.20"); err != nil || got.Name != "dev-code" {
		t.Errorf("the redeemed token: %v", err)
	}
	if _, err := w.svc.Authenticate(ctx, before, "x"); !Is(err, Unauthorized) {
		t.Errorf("the token before the link still works: %v", err)
	}
	if _, _, err := w.svc.RedeemAgentLink(ctx, code, "x"); !Is(err, Spent) {
		t.Errorf("redeemed twice: %v", err)
	}
}

// Two requests with one link at the same moment: exactly one gets through.
func TestALinkIsUsedOnceUnderARace(t *testing.T) {
	w := newWorld(t)
	code, _, _ := w.svc.IssueLink(ctx, w.admin, w.devCode.Identity, 0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := w.svc.RedeemAgentLink(ctx, code, "x"); err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Errorf("%d redemptions succeeded, want 1", won)
	}
}

func TestRemovingSomeoneEndsTheirWayIn(t *testing.T) {
	w := newWorld(t)
	cookie := w.signIn(w.camille)
	waiting, _, _ := w.svc.IssueLink(ctx, w.admin, w.camille.Identity, 0)
	if err := w.svc.DeleteIdentity(ctx, w.admin, w.camille.Identity); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.svc.Session(ctx, ChatSurface, cookie, "x"); err == nil {
		t.Error("a removed person's sign-in still works")
	}
	if _, _, err := w.svc.SignInWithLink(ctx, waiting, "x", ""); err == nil {
		t.Error("a removed person's link still works")
	}
}

func TestDescribeAgent(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0":                                              "Firefox on Linux",
		"Mozilla/5.0 (Linux; Android 15; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Mobile Safari/537.36":   "Chrome on Android",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1": "Safari on iPhone",
		"curl/8.5.0": "curl",
		"":           "a browser",
	} {
		if got := describeAgent(ua); got != want {
			t.Errorf("describeAgent(%q) = %q, want %q", ua, got, want)
		}
	}
}
