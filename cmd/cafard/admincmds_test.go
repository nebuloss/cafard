package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/clock"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/store"
)

func runWithStdin(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut strings.Builder
	err := run(context.Background(), args, strings.NewReader(stdin), &out, &errOut, func(string) string { return "" })
	return out.String(), errOut.String(), err
}

func openData(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestPasswdSetsThePasswordFromAPipe(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runWithStdin(t, "short\n", "passwd", "-data-dir", dir); err == nil || !strings.Contains(err.Error(), "8 characters") {
		t.Errorf("short password: %v", err)
	}
	out, _, err := runWithStdin(t, "correct horse\n", "passwd", "-data-dir", dir)
	if err != nil || !strings.Contains(out, "end when the server restarts") {
		t.Fatalf("passwd = %q, %v", out, err)
	}
	out, _, err = runWithStdin(t, "another password\n", "passwd", "--if-unset", "-data-dir", dir)
	if err != nil || !strings.Contains(out, "left alone") {
		t.Errorf("--if-unset over a password = %q, %v", out, err)
	}
	st := openData(t, dir)
	admin := auth.NewAdmin(st.DB(), clock.System{})
	if _, err := admin.SignIn(context.Background(), "127.0.0.1", "correct horse"); err != nil {
		t.Errorf("the piped password does not sign in: %v", err)
	}
	if p, err := auth.NewTokens(st.DB(), clock.System{}).Principal(context.Background(), auth.AdminName); err != nil || p.Kind != auth.KindHuman {
		t.Errorf("the admin principal: %+v %v", p, err)
	}
}

func TestPairPrintsALinkThatSignsIn(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runWithStdin(t, "", "pair", "--name", "guillaume-phone", "--scopes", "read,admin", "-data-dir", dir); err == nil {
		t.Error("a link with the admin scope")
	}
	if _, _, err := runWithStdin(t, "", "pair", "--name", "guillaume-phone", "--ttl", "25h", "-data-dir", dir); err == nil {
		t.Error("a link for more than a day")
	}
	out, msg, err := runWithStdin(t, "", "pair", "--name", "guillaume-phone", "--ttl", "2h", "-data-dir", dir, "-public-url", "https://cafard.example/")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`^https://cafard\.example/p/([a-z0-9]{16})\n$`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(msg, "guillaume-phone") || !strings.Contains(msg, "ingest read message") {
		t.Fatalf("pair = %q %q", out, msg)
	}
	st := openData(t, dir)
	sessions := auth.NewSessions(st.DB(), clock.System{})
	row, _, err := auth.NewPairings(st.DB(), clock.System{}, sessions, nil).Redeem(context.Background(), m[1], "")
	if err != nil || row.Principal != "guillaume-phone" || row.Kind != auth.KindHuman {
		t.Errorf("redeem = %+v %v", row, err)
	}
}

func TestTokenAndPairGrantProjects(t *testing.T) {
	dir := t.TempDir()
	st := openData(t, dir)
	ctx := context.Background()
	projs, err := projects.New(ctx, st.DB(), clock.System{}, store.Limits{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projs.Create(ctx, "lab", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runWithStdin(t, "", "token", "create", "--name", "codex", "--scopes", "read", "--grant", "nowhere=read", "-data-dir", dir); err == nil {
		t.Error("a grant on a project that does not exist")
	}
	if _, _, err := runWithStdin(t, "", "token", "create", "--name", "codex", "--scopes", "read", "--grant", "lab", "-data-dir", dir); err == nil {
		t.Error("a grant without scopes")
	}
	_, msg, err := runWithStdin(t, "", "token", "create", "--name", "codex", "--scopes", "read,message", "--grant", "lab=read,message", "-data-dir", dir)
	if err != nil || !strings.Contains(msg, "grants lab=read,message") {
		t.Fatalf("token create = %q %v", msg, err)
	}
	if gs, err := projs.Grants(ctx, "lab", "codex"); err != nil || len(gs) != 1 || gs[0].Scopes.String() != "read message" {
		t.Errorf("grants = %+v %v", gs, err)
	}

	out, _, err := runWithStdin(t, "", "pair", "--name", "phone", "--grant", "lab=read", "-data-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimSpace(out[strings.LastIndex(out, "/p/")+3:])
	if gs, _ := projs.Grants(ctx, "lab", "phone"); len(gs) != 0 {
		t.Error("the grant is there before the link is opened")
	}
	if _, _, err := auth.NewPairings(st.DB(), clock.System{}, auth.NewSessions(st.DB(), clock.System{}), projs).Redeem(ctx, code, ""); err != nil {
		t.Fatal(err)
	}
	if gs, err := projs.Grants(ctx, "lab", "phone"); err != nil || len(gs) != 1 || gs[0].Scopes.String() != "read" {
		t.Errorf("grants after redeeming = %+v %v", gs, err)
	}
}
