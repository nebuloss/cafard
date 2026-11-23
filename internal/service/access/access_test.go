package access

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"cafard/internal/store"
)

var ctx = context.Background()

// world is the cast of the design doc: an admin, a colleague, two agents, a
// device, and two projects.
type world struct {
	t                           *testing.T
	st                          *store.Store
	svc                         *Service
	admin, guillaume, camille   Who
	devCode, labBench, devBuild Who
	be98, zeeos                 store.Project
}

func newWorld(t *testing.T) *world {
	t.Helper()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cafard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := &world{t: t, st: st, svc: New(st)}
	boot := store.AuditEntry{Actor: "cafard", Action: "bootstrap", Target: "test"}
	mk := func(name string, kind store.Kind, admin bool) Who {
		i, err := st.CreateIdentity(ctx, store.Identity{Name: name, Kind: kind, Admin: admin}, boot)
		if err != nil {
			t.Fatal(err)
		}
		return Who{Identity: i, Origin: "test"}
	}
	w.admin = Admin("test")
	w.guillaume = mk("guillaume", store.Person, false)
	w.camille = mk("camille", store.Person, false)
	w.devCode = mk("dev-code", store.Agent, false)
	w.labBench = mk("lab-bench", store.Agent, false)
	w.devBuild = mk("dev-build", store.Device, false)
	w.be98 = w.project("be98")
	w.zeeos = w.project("zeeos")
	w.in(w.be98, w.guillaume, w.devCode, w.labBench, w.devBuild)
	w.in(w.zeeos, w.camille, w.devCode)
	return w
}

func (w *world) project(name string) store.Project {
	w.t.Helper()
	p, err := w.svc.CreateProject(ctx, w.admin, name, 30, 5e9)
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func (w *world) in(p store.Project, members ...Who) {
	w.t.Helper()
	for _, m := range members {
		if err := w.svc.AddMember(ctx, w.admin, p, m.Identity); err != nil {
			w.t.Fatal(err)
		}
	}
}

// reload reads who again, after a change to it.
func (w *world) reload(who Who) Who {
	w.t.Helper()
	i, err := w.st.Identity(ctx, who.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return Who{Identity: i, Origin: who.Origin}
}

func (w *world) reloadProject(p store.Project) store.Project {
	w.t.Helper()
	got, err := w.st.Project(ctx, p.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

func wantCode(t *testing.T, what string, err error, code Code) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Errorf("%s: refused (%v), want allowed", what, err)
		}
		return
	}
	if !Is(err, code) {
		t.Errorf("%s: err = %v, want refusal %s", what, err, code)
	}
}

func TestMembershipIsTheOnlyPermission(t *testing.T) {
	w := newWorld(t)
	for _, tc := range []struct {
		what string
		err  error
		code Code
	}{
		{"guillaume reads be98", w.svc.CanRead(ctx, w.guillaume, w.be98), ""},
		{"dev-code reads be98", w.svc.CanRead(ctx, w.devCode, w.be98), ""},
		{"dev-code posts in zeeos", w.svc.CanPost(ctx, w.devCode, w.zeeos), ""},
		{"lab-bench reads zeeos", w.svc.CanRead(ctx, w.labBench, w.zeeos), NotMember},
		{"camille reads be98", w.svc.CanRead(ctx, w.camille, w.be98), NotMember},
		// An admin manages who is where; it does not read what it is not in.
		{"guillaume (admin) reads zeeos", w.svc.CanRead(ctx, w.guillaume, w.zeeos), NotMember},
	} {
		wantCode(t, tc.what, tc.err, tc.code)
	}
}

func TestDevicesOnlySend(t *testing.T) {
	w := newWorld(t)
	wantCode(t, "dev-build posts to be98", w.svc.CanPost(ctx, w.devBuild, w.be98), "")
	wantCode(t, "dev-build reads be98", w.svc.CanRead(ctx, w.devBuild, w.be98), SendOnly)
	wantCode(t, "dev-build posts to zeeos", w.svc.CanPost(ctx, w.devBuild, w.zeeos), NotMember)
	wantCode(t, "dev-build writes to guillaume", w.svc.CanDirectMessage(ctx, w.devBuild, w.guillaume.Identity), SendOnly)
	wantCode(t, "guillaume writes to dev-build", w.svc.CanDirectMessage(ctx, w.guillaume, w.devBuild.Identity), SendOnly)
}

// Rule 1: no agent or device can change access.
func TestNoAgentOrDeviceCanChangeAccess(t *testing.T) {
	w := newWorld(t)
	for _, m := range []Who{w.devCode, w.devBuild} {
		wantCode(t, m.Name+" adds lab-bench to zeeos", w.svc.AddMember(ctx, m, w.zeeos, w.labBench.Identity), NotAllowed)
		wantCode(t, m.Name+" adds itself to zeeos", w.svc.AddMember(ctx, m, w.zeeos, m.Identity), NotAllowed)
		wantCode(t, m.Name+" removes guillaume from be98", w.svc.RemoveMember(ctx, m, w.be98, w.guillaume.Identity), NotAllowed)
		_, err := w.svc.CreateProject(ctx, m, "mine", 30, 5e9)
		wantCode(t, m.Name+" creates a project", err, NotAllowed)
		_, err = w.svc.CreateIdentity(ctx, m, NewIdentity{Kind: store.Agent, Name: "helper"})
		wantCode(t, m.Name+" creates an agent", err, NotAllowed)
		wantCode(t, m.Name+" pauses camille", w.svc.SetPaused(ctx, m, w.camille.Identity, true), NotAllowed)
		wantCode(t, m.Name+" deletes camille", w.svc.DeleteIdentity(ctx, m, w.camille.Identity), NotAllowed)
		wantCode(t, m.Name+" archives be98", w.svc.SetArchived(ctx, m, w.be98, true), NotAllowed)
	}
	if ok, _ := w.st.IsMember(ctx, w.zeeos.ID, w.labBench.ID); ok {
		t.Error("lab-bench ended up in zeeos")
	}
}

// Rule 2: only people add members, a person in the project, or the admin.
func TestOnlyPeopleInTheProjectOrAdminsAddMembers(t *testing.T) {
	w := newWorld(t)
	// camille is in zeeos and not an admin: she may add to zeeos, not to be98.
	wantCode(t, "camille adds lab-bench to zeeos", w.svc.AddMember(ctx, w.camille, w.zeeos, w.labBench.Identity), "")
	wantCode(t, "camille adds herself to be98", w.svc.AddMember(ctx, w.camille, w.be98, w.camille.Identity), NotMember)
	wantCode(t, "camille removes dev-code from be98", w.svc.RemoveMember(ctx, w.camille, w.be98, w.devCode.Identity), NotMember)
	// guillaume is not in zeeos: he may not; the admin may.
	wantCode(t, "guillaume removes lab-bench from zeeos", w.svc.RemoveMember(ctx, w.guillaume, w.zeeos, w.labBench.Identity), NotMember)
	wantCode(t, "the admin removes lab-bench from zeeos", w.svc.RemoveMember(ctx, w.admin, w.zeeos, w.labBench.Identity), "")
	// Admin-only actions stay admin-only for camille.
	_, err := w.svc.CreateProject(ctx, w.camille, "camille-only", 30, 5e9)
	wantCode(t, "camille creates a project", err, NotAdmin)

	wantCode(t, "adding dev-code to be98 again", w.svc.AddMember(ctx, w.guillaume, w.be98, w.devCode.Identity), Exists)
	wantCode(t, "removing camille from be98", w.svc.RemoveMember(ctx, w.guillaume, w.be98, w.camille.Identity), NotMember)
}

// Rule 3: a direct message needs a project in common.
func TestDirectMessagesNeedASharedProject(t *testing.T) {
	w := newWorld(t)
	wantCode(t, "dev-code writes to lab-bench (be98)", w.svc.CanDirectMessage(ctx, w.devCode, w.labBench.Identity), "")
	wantCode(t, "dev-code writes to camille (zeeos)", w.svc.CanDirectMessage(ctx, w.devCode, w.camille.Identity), "")
	wantCode(t, "lab-bench writes to camille", w.svc.CanDirectMessage(ctx, w.labBench, w.camille.Identity), NoSharedProject)
	wantCode(t, "camille writes to lab-bench", w.svc.CanDirectMessage(ctx, w.camille, w.labBench.Identity), NoSharedProject)
	wantCode(t, "dev-code writes to itself", w.svc.CanDirectMessage(ctx, w.devCode, w.devCode.Identity), Self)
}

// Rule 4: removing a member keeps its row and name, and takes its access.
func TestRemovedMembersKeepTheirNameAndLoseAccess(t *testing.T) {
	w := newWorld(t)
	if err := w.svc.DeleteIdentity(ctx, w.admin, w.labBench.Identity); err != nil {
		t.Fatal(err)
	}
	lb := w.reload(w.labBench)
	if !lb.Deleted() || lb.Name != "lab-bench" {
		t.Fatalf("after delete: %+v", lb.Identity)
	}
	wantCode(t, "removed lab-bench reads be98", w.svc.CanRead(ctx, lb, w.be98), Removed)
	wantCode(t, "dev-code writes to removed lab-bench", w.svc.CanDirectMessage(ctx, w.devCode, lb.Identity), Removed)
	wantCode(t, "guillaume adds removed lab-bench back", w.svc.AddMember(ctx, w.guillaume, w.be98, lb.Identity), Removed)
	_, err := w.svc.CreateIdentity(ctx, w.admin, NewIdentity{Kind: store.Agent, Name: "lab-bench"})
	wantCode(t, "a new lab-bench", err, Exists)
}

func TestPausedMembersCanDoNothing(t *testing.T) {
	w := newWorld(t)
	if err := w.svc.SetPaused(ctx, w.admin, w.devCode.Identity, true); err != nil {
		t.Fatal(err)
	}
	dc := w.reload(w.devCode)
	wantCode(t, "paused dev-code reads be98", w.svc.CanRead(ctx, dc, w.be98), Paused)
	wantCode(t, "paused dev-code posts in be98", w.svc.CanPost(ctx, dc, w.be98), Paused)
	if err := w.svc.SetPaused(ctx, w.admin, dc.Identity, false); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "resumed dev-code reads be98", w.svc.CanRead(ctx, w.reload(dc), w.be98), "")
}

func TestArchivedProjectsAreReadOnly(t *testing.T) {
	w := newWorld(t)
	if err := w.svc.SetArchived(ctx, w.admin, w.be98, true); err != nil {
		t.Fatal(err)
	}
	be98 := w.reloadProject(w.be98)
	wantCode(t, "dev-code reads archived be98", w.svc.CanRead(ctx, w.devCode, be98), "")
	wantCode(t, "dev-code posts in archived be98", w.svc.CanPost(ctx, w.devCode, be98), Archived)
	wantCode(t, "guillaume adds camille to archived be98", w.svc.AddMember(ctx, w.guillaume, be98, w.camille.Identity), Archived)
}

// The admin manages access and is not a member: it reads and posts
// nowhere, and a person is never an admin.
func TestTheAdminIsNotAMember(t *testing.T) {
	w := newWorld(t)
	wantCode(t, "the admin reads be98", w.svc.CanRead(ctx, w.admin, w.be98), NotMember)
	wantCode(t, "the admin posts in be98", w.svc.CanPost(ctx, w.admin, w.be98), NotMember)
	_, err := w.svc.CreateProject(ctx, w.guillaume, "speedtest", 30, 5e9)
	wantCode(t, "a person creates a project", err, NotAdmin)
	_, err = w.svc.CreateIdentity(ctx, w.guillaume, NewIdentity{Kind: store.Agent, Name: "qa-bench"})
	wantCode(t, "a person creates an agent", err, NotAdmin)
	wantCode(t, "the admin pauses guillaume", w.svc.SetPaused(ctx, w.admin, w.guillaume.Identity, true), "")
}

func TestNamesAndValues(t *testing.T) {
	w := newWorld(t)
	for _, tc := range []struct {
		n    NewIdentity
		code Code
	}{
		{NewIdentity{Kind: store.Agent, Name: "qa-bench"}, ""},
		{NewIdentity{Kind: store.Person, Name: "ines.d"}, ""},
		{NewIdentity{Kind: store.Agent, Name: "QA bench"}, BadName},
		{NewIdentity{Kind: store.Agent, Name: "x"}, BadName},
		{NewIdentity{Kind: store.Device, Name: "dot.ted"}, BadName},
		{NewIdentity{Kind: "robot", Name: "r2"}, BadValue},
	} {
		_, err := w.svc.CreateIdentity(ctx, w.admin, tc.n)
		wantCode(t, "create "+string(tc.n.Kind)+" "+tc.n.Name, err, tc.code)
	}
	_, err := w.svc.CreateProject(ctx, w.admin, "Speed Test", 30, 5e9)
	wantCode(t, "project with spaces", err, BadName)
	_, err = w.svc.CreateProject(ctx, w.admin, "speedtest", 0, 5e9)
	wantCode(t, "project with no retention", err, BadValue)
	_, err = w.svc.CreateProject(ctx, w.admin, "be98", 30, 5e9)
	wantCode(t, "a second be98", err, Exists)
}

// Refusals are read by people and by agents; each says what to do next where
// there is something to do.
func TestRefusalsSayWhatToDo(t *testing.T) {
	w := newWorld(t)
	err := w.svc.CanRead(ctx, w.labBench, w.zeeos)
	if err == nil || !strings.Contains(err.Error(), "Ask a person in the project to add it") {
		t.Errorf("not a member: %q", err)
	}
	err = w.svc.AddMember(ctx, w.devCode, w.zeeos, w.labBench.Identity)
	if err == nil || !strings.Contains(err.Error(), "only people") {
		t.Errorf("agent adding a member: %q", err)
	}
}

// Every change goes in the audit log, under the name of who made it and
// where from.
func TestChangesAreAuditedUnderWhoMadeThem(t *testing.T) {
	w := newWorld(t)
	w.camille.Origin = "chat · 10.0.50.44"
	if err := w.svc.AddMember(ctx, w.camille, w.zeeos, w.labBench.Identity); err != nil {
		t.Fatal(err)
	}
	log, err := w.st.Audit(ctx, 1)
	if err != nil || len(log) != 1 {
		t.Fatalf("audit: %v %v", log, err)
	}
	got := log[0]
	if got.Actor != "camille" || got.Action != "added lab-bench to" || got.Target != "zeeos" || got.Origin != "chat · 10.0.50.44" {
		t.Errorf("audit line %+v", got)
	}
}
