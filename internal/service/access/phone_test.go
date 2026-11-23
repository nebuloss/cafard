package access

import (
	"testing"
)

func TestSignInAnotherPhone(t *testing.T) {
	w := newWorld(t)
	w.signIn(w.camille)
	camille := w.reload(w.camille)

	_, _, err := w.svc.IssueSigninLink(ctx, w.devCode)
	wantCode(t, "a code for an agent", err, NotAllowed)

	code, l, err := w.svc.IssueSigninLink(ctx, camille)
	if err != nil {
		t.Fatal(err)
	}
	if l.ExpiresAt.Sub(l.CreatedAt) > SigninLinkLife {
		t.Errorf("the code lasts %v", l.ExpiresAt.Sub(l.CreatedAt))
	}
	// A sign-in link sent by an admin meanwhile is not cancelled by the code,
	// nor the code by it.
	adminCode, _, _ := w.svc.IssueLink(ctx, w.admin, camille.Identity, 0)

	id, cookie, err := w.svc.RedeemSigninLink(ctx, code, "10.0.50.130", "Mozilla/5.0 (iPhone) Safari/604")
	if err != nil || id.Name != "camille" {
		t.Fatalf("redeem: %v", err)
	}
	if got, _, err := w.svc.Session(ctx, ChatSurface, cookie, "10.0.50.130"); err != nil || got.Name != "camille" {
		t.Errorf("the phone's sign-in: %v", err)
	}
	if _, _, err := w.svc.RedeemSigninLink(ctx, code, "x", ""); !Is(err, Spent) {
		t.Errorf("the code used twice: %v", err)
	}
	if _, _, err := w.svc.SignInWithLink(ctx, adminCode, "x", ""); err != nil {
		t.Errorf("the admin's sign-in link after a phone's code: %v", err)
	}
	// A join link is not a sign-in code.
	other, _, _ := w.svc.IssueLink(ctx, w.admin, w.labBench.Identity, 0)
	if _, _, err := w.svc.RedeemSigninLink(ctx, other, "x", ""); !Is(err, NotAllowed) {
		t.Errorf("an install link used to sign in: %v", err)
	}
}

func TestWhoMayDeleteAFile(t *testing.T) {
	w := newWorld(t)
	wantCode(t, "the sender", w.svc.CanDeleteFile(ctx, w.devBuild, w.devBuild.ID), "")
	wantCode(t, "another member", w.svc.CanDeleteFile(ctx, w.devCode, w.devBuild.ID), NotAllowed)
	wantCode(t, "a person", w.svc.CanDeleteFile(ctx, w.camille, w.devBuild.ID), NotAllowed)
}
