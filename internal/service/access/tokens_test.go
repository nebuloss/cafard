package access

import (
	"strings"
	"testing"
)

func TestTokensAuthenticateTheirIdentity(t *testing.T) {
	w := newWorld(t)
	agentTok, err := w.svc.IssueToken(ctx, w.admin, w.devCode.Identity)
	if err != nil {
		t.Fatal(err)
	}
	deviceTok, err := w.svc.IssueToken(ctx, w.admin, w.devBuild.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(agentTok, "cfd_ag_") || !strings.HasPrefix(deviceTok, "cfd_dv_") {
		t.Errorf("prefixes: %q %q", agentTok, deviceTok)
	}
	if len(agentTok) != len("cfd_ag_")+52 {
		t.Errorf("token length %d: want 32 random bytes in base32", len(agentTok))
	}
	for tok, want := range map[string]string{agentTok: "dev-code", deviceTok: "dev-build"} {
		id, err := w.svc.Authenticate(ctx, tok, "10.0.50.20")
		if err != nil || id.Name != want {
			t.Errorf("authenticate %s: %+v %v", want, id, err)
		}
	}
}

func TestOnlyAdminsIssueTokensAndOnlyToMachines(t *testing.T) {
	w := newWorld(t)
	_, err := w.svc.IssueToken(ctx, w.devCode, w.labBench.Identity)
	wantCode(t, "an agent issues a token", err, NotAllowed)
	_, err = w.svc.IssueToken(ctx, w.camille, w.labBench.Identity)
	wantCode(t, "a non-admin issues a token", err, NotAdmin)
	_, err = w.svc.IssueToken(ctx, w.admin, w.camille.Identity)
	wantCode(t, "a token for a person", err, NotAllowed)
}

func TestTokensThatMustNotWork(t *testing.T) {
	w := newWorld(t)
	old, _ := w.svc.IssueToken(ctx, w.admin, w.devCode.Identity)
	current, _ := w.svc.IssueToken(ctx, w.admin, w.devCode.Identity)

	_, err := w.svc.Authenticate(ctx, old, "x")
	wantCode(t, "a replaced token", err, Unauthorized)
	_, err = w.svc.Authenticate(ctx, "Bearer abc", "x")
	wantCode(t, "not a cafard token", err, Unauthorized)
	_, err = w.svc.Authenticate(ctx, "cfd_dv_"+strings.TrimPrefix(current, "cfd_ag_"), "x")
	wantCode(t, "an agent token relabelled as a device's", err, Unauthorized)

	if err := w.svc.SetPaused(ctx, w.admin, w.devCode.Identity, true); err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.Authenticate(ctx, current, "x")
	wantCode(t, "the token of a paused agent", err, Paused)

	if err := w.svc.DeleteIdentity(ctx, w.admin, w.devCode.Identity); err != nil {
		t.Fatal(err)
	}
	// Removing an agent deletes its token, so it is not a token any more.
	_, err = w.svc.Authenticate(ctx, current, "x")
	wantCode(t, "the token of a removed agent", err, Unauthorized)
}

// The admin password: a hash to keep in the configuration, which only the
// right password matches.
func TestAdminPassword(t *testing.T) {
	if _, err := HashAdminPassword("short"); !Is(err, BadValue) {
		t.Errorf("a short password: %v", err)
	}
	hash, err := HashAdminPassword("Admin000")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckAdminPassword(hash, "Admin000") || CheckAdminPassword(hash, "Admin001") || CheckAdminPassword("", "Admin000") {
		t.Error("the admin password checks wrongly")
	}
}
