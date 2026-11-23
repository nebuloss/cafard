package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cafard.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

var ctx = context.Background()

func audit(action string) AuditEntry {
	return AuditEntry{Actor: "test", Action: action, Target: "t", Origin: "test"}
}

func TestOpenTwiceKeepsTheSchema(t *testing.T) {
	s, path := open(t)
	if _, err := s.CreateProject(ctx, Project{Name: "be98", RetentionDays: 30, QuotaBytes: 5e9}, audit("create")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	again, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	if _, err := again.ProjectByName(ctx, "be98"); err != nil {
		t.Errorf("the project did not survive a reopen: %v", err)
	}
}

func TestDatabaseFileIsPrivate(t *testing.T) {
	_, path := open(t)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("database mode %v, want 0600", st.Mode().Perm())
	}
}

func TestNewerDatabaseIsRefused(t *testing.T) {
	s, path := open(t)
	if _, err := s.db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (999, 0)`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(ctx, path); err == nil {
		t.Fatal("a database from a newer release was opened")
	}
}

// One namespace: @name must mean one member, across people, agents and
// devices, deleted ones included.
func TestNamesAreUniqueAcrossKinds(t *testing.T) {
	s, _ := open(t)
	agent, err := s.CreateIdentity(ctx, Identity{Name: "dev-code", Kind: Agent}, audit("create"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIdentity(ctx, Identity{Name: "dev-code", Kind: Person}, audit("create")); !errors.Is(err, ErrExists) {
		t.Errorf("a person named like an agent: %v, want ErrExists", err)
	}
	if err := s.DeleteIdentity(ctx, agent.ID, audit("delete")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIdentity(ctx, Identity{Name: "dev-code", Kind: Agent}, audit("create")); !errors.Is(err, ErrExists) {
		t.Errorf("a deleted identity's name was reused: %v", err)
	}
}

func TestMemberships(t *testing.T) {
	s, _ := open(t)
	g, _ := s.CreateIdentity(ctx, Identity{Name: "guillaume", Kind: Person, Admin: true}, audit("create"))
	dc, _ := s.CreateIdentity(ctx, Identity{Name: "dev-code", Kind: Agent}, audit("create"))
	lb, _ := s.CreateIdentity(ctx, Identity{Name: "lab-bench", Kind: Agent}, audit("create"))
	be98, _ := s.CreateProject(ctx, Project{Name: "be98", RetentionDays: 30, QuotaBytes: 5e9}, audit("create"))
	zeeos, _ := s.CreateProject(ctx, Project{Name: "zeeos", RetentionDays: 30, QuotaBytes: 5e9}, audit("create"))

	for _, m := range []struct{ p, i int64 }{{be98.ID, g.ID}, {be98.ID, dc.ID}, {zeeos.ID, dc.ID}, {zeeos.ID, lb.ID}} {
		if err := s.AddMembership(ctx, m.p, m.i, g.ID, audit("add")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddMembership(ctx, be98.ID, dc.ID, g.ID, audit("add")); !errors.Is(err, ErrExists) {
		t.Errorf("adding a member twice: %v, want ErrExists", err)
	}

	if ok, _ := s.IsMember(ctx, be98.ID, lb.ID); ok {
		t.Error("lab-bench is in be98")
	}
	if ok, _ := s.ShareProject(ctx, g.ID, lb.ID); ok {
		t.Error("guillaume and lab-bench share no project")
	}
	if ok, _ := s.ShareProject(ctx, dc.ID, lb.ID); !ok {
		t.Error("dev-code and lab-bench share zeeos")
	}

	ps, err := s.ProjectsOf(ctx, dc.ID)
	if err != nil || len(ps) != 2 || ps[0].Name != "be98" || ps[1].Name != "zeeos" {
		t.Errorf("ProjectsOf(dev-code) = %+v, %v", ps, err)
	}
	ms, err := s.Members(ctx, be98.ID)
	if err != nil || len(ms) != 2 || ms[0].Name != "dev-code" || ms[1].Name != "guillaume" || ms[0].Kind != Agent {
		t.Errorf("Members(be98) = %+v, %v", ms, err)
	}

	if err := s.RemoveMembership(ctx, be98.ID, dc.ID, audit("remove")); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMembership(ctx, be98.ID, dc.ID, audit("remove")); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing a non-member: %v, want ErrNotFound", err)
	}
}

// Deleting keeps the row, for history, and takes the memberships, so the
// identity reaches nothing.
func TestDeleteIdentityKeepsHistoryAndDropsAccess(t *testing.T) {
	s, _ := open(t)
	g, _ := s.CreateIdentity(ctx, Identity{Name: "guillaume", Kind: Person, Admin: true}, audit("create"))
	dc, _ := s.CreateIdentity(ctx, Identity{Name: "dev-code", Kind: Agent}, audit("create"))
	p, _ := s.CreateProject(ctx, Project{Name: "be98", RetentionDays: 30, QuotaBytes: 5e9}, audit("create"))
	s.AddMembership(ctx, p.ID, dc.ID, g.ID, audit("add"))

	if err := s.DeleteIdentity(ctx, dc.ID, audit("delete")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Identity(ctx, dc.ID)
	if err != nil {
		t.Fatalf("a deleted identity's row is gone: %v", err)
	}
	if !got.Deleted() || got.Enabled {
		t.Errorf("after delete: %+v", got)
	}
	if ok, _ := s.IsMember(ctx, p.ID, dc.ID); ok {
		t.Error("a deleted identity kept its membership")
	}
	if err := s.DeleteIdentity(ctx, dc.ID, audit("delete")); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice: %v, want ErrNotFound", err)
	}
}

func TestArchiveAndDeleteProject(t *testing.T) {
	s, _ := open(t)
	g, _ := s.CreateIdentity(ctx, Identity{Name: "guillaume", Kind: Person, Admin: true}, audit("create"))
	p, _ := s.CreateProject(ctx, Project{Name: "zeeos-qa", RetentionDays: 90, QuotaBytes: 5e9}, audit("create"))
	s.AddMembership(ctx, p.ID, g.ID, g.ID, audit("add"))

	if err := s.SetArchived(ctx, p.ID, true, audit("archive")); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Project(ctx, p.ID); !got.Archived() {
		t.Error("not archived")
	}
	if err := s.SetArchived(ctx, p.ID, false, audit("unarchive")); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Project(ctx, p.ID); got.Archived() {
		t.Error("still archived")
	}

	if err := s.DeleteProject(ctx, p.ID, audit("delete")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Project(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted project: %v", err)
	}
	if ps, _ := s.ProjectsOf(ctx, g.ID); len(ps) != 0 {
		t.Errorf("a deleted project's memberships stayed: %+v", ps)
	}
}

// The change and its audit line are one transaction: a change that fails
// writes no audit line, and one that succeeds always writes one.
func TestEveryChangeIsAuditedWithIt(t *testing.T) {
	s, _ := open(t)
	if _, err := s.CreateProject(ctx, Project{Name: "be98", RetentionDays: 30, QuotaBytes: 5e9},
		AuditEntry{Actor: "guillaume", Action: "created project", Target: "be98", Origin: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(ctx, Project{Name: "be98", RetentionDays: 30, QuotaBytes: 5e9},
		AuditEntry{Actor: "guillaume", Action: "created project", Target: "be98 again", Origin: "admin"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate project: %v", err)
	}
	if err := s.SetArchived(ctx, 999, true, audit("archived a project that does not exist")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive missing: %v", err)
	}

	log, err := s.Audit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 || log[0].Actor != "guillaume" || log[0].Target != "be98" || log[0].Origin != "admin" {
		t.Errorf("audit = %+v, want only the change that happened", log)
	}
}
