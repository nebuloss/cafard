package store

import (
	"errors"
	"testing"
	"time"
)

type fixture struct {
	s         *Store
	guillaume Identity
	devCode   Identity
	be98      Project
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	s, _ := open(t)
	g, err := s.CreateFirstAdmin(ctx, Identity{Name: "guillaume"}, audit("bootstrap"))
	if err != nil {
		t.Fatal(err)
	}
	dc, _ := s.CreateIdentity(ctx, Identity{Name: "dev-code", Kind: Agent}, audit("create"))
	p, _ := s.CreateProject(ctx, Project{Name: "be98", RetentionDays: 30, QuotaBytes: 5e9}, audit("create"))
	return fixture{s: s, guillaume: g, devCode: dc, be98: p}
}

func TestFirstAdminOnlyOnce(t *testing.T) {
	f := newFixture(t)
	if !f.guillaume.Admin || f.guillaume.Kind != Person {
		t.Fatalf("first admin: %+v", f.guillaume)
	}
	if _, err := f.s.CreateFirstAdmin(ctx, Identity{Name: "intruder"}, audit("bootstrap")); !errors.Is(err, ErrHasAdmin) {
		t.Errorf("a second first admin: %v, want ErrHasAdmin", err)
	}
	if _, err := f.s.IdentityByName(ctx, "intruder"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the refused first admin was created anyway: %v", err)
	}
}

func TestTokens(t *testing.T) {
	f := newFixture(t)
	if err := f.s.SetToken(ctx, f.devCode.ID, "hash-1", audit("token")); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.IdentityByTokenHash(ctx, "hash-1")
	if err != nil || got.Name != "dev-code" {
		t.Fatalf("by hash: %+v %v", got, err)
	}

	if err := f.s.TouchToken(ctx, f.devCode.ID, "10.0.50.20"); err != nil {
		t.Fatal(err)
	}
	u, _ := f.s.TokenUse(ctx, f.devCode.ID)
	if u.LastUsed == nil || u.From != "10.0.50.20" {
		t.Errorf("after a use: %+v", u)
	}

	// Regenerating replaces: the old hash finds nobody, and last-used resets.
	if err := f.s.SetToken(ctx, f.devCode.ID, "hash-2", audit("token")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.IdentityByTokenHash(ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old token still works: %v", err)
	}
	if u, _ := f.s.TokenUse(ctx, f.devCode.ID); u.LastUsed != nil {
		t.Errorf("a new token was already used: %+v", u)
	}
}

func TestTouchIsThrottled(t *testing.T) {
	f := newFixture(t)
	f.s.SetToken(ctx, f.devCode.ID, "h", audit("token"))
	start := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	f.s.now = func() time.Time { return start }
	f.s.TouchToken(ctx, f.devCode.ID, "a")
	f.s.now = func() time.Time { return start.Add(10 * time.Second) }
	f.s.TouchToken(ctx, f.devCode.ID, "a")
	if u, _ := f.s.TokenUse(ctx, f.devCode.ID); !u.LastUsed.Equal(start) {
		t.Errorf("a touch 10 s later wrote: %v", u.LastUsed)
	}
	f.s.now = func() time.Time { return start.Add(2 * time.Minute) }
	f.s.TouchToken(ctx, f.devCode.ID, "a")
	if u, _ := f.s.TokenUse(ctx, f.devCode.ID); !u.LastUsed.Equal(start.Add(2 * time.Minute)) {
		t.Errorf("a touch 2 min later did not write: %v", u.LastUsed)
	}
}

func TestConversationsAndMessages(t *testing.T) {
	f := newFixture(t)
	c, created, err := f.s.EnsureConversation(ctx, f.be98.ID, "wifi-drops", "wifi drops", f.devCode.ID)
	if err != nil || !created {
		t.Fatalf("ensure: %+v %v %v", c, created, err)
	}
	again, created, err := f.s.EnsureConversation(ctx, f.be98.ID, "wifi-drops", "another title", f.devCode.ID)
	if err != nil || created || again.ID != c.ID || again.Title != "wifi drops" {
		t.Errorf("ensure twice: %+v %v %v", again, created, err)
	}

	m1, err := f.s.AddMessage(ctx, c.ID, f.guillaume.ID, "LED blinks orange", nil)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := f.s.AddMessage(ctx, c.ID, f.devCode.ID, "", []NewFile{{Name: "boot.log", ContentType: "text/plain"}})
	if err != nil || len(m2.Files) != 1 || m2.Files[0].State != Live {
		t.Fatalf("message with a file: %+v %v", m2, err)
	}
	if err := f.s.StoreFile(ctx, m2.Files[0].ID, "abc", 42); err != nil {
		t.Fatal(err)
	}

	ms, err := f.s.Messages(ctx, c.ID, 0, 10)
	if err != nil || len(ms) != 2 {
		t.Fatalf("messages: %+v %v", ms, err)
	}
	if ms[0].ID != m1.ID || ms[0].Author.Name != "guillaume" || ms[1].Author.Kind != Agent {
		t.Errorf("order or authors: %+v", ms)
	}
	if fl := ms[1].Files; len(fl) != 1 || fl[0].State != Stored || fl[0].SHA256 != "abc" || fl[0].Size != 42 {
		t.Errorf("file after StoreFile: %+v", fl)
	}
	if after, _ := f.s.Messages(ctx, c.ID, m1.ID, 10); len(after) != 1 || after[0].ID != m2.ID {
		t.Errorf("after m1: %+v", after)
	}

	fw, err := f.s.File(ctx, m2.Files[0].ID)
	if err != nil || fw.ProjectID != f.be98.ID || fw.ConversationID != c.ID {
		t.Errorf("file location: %+v %v", fw, err)
	}

	cs, err := f.s.Conversations(ctx, f.be98.ID)
	if err != nil || len(cs) != 1 || cs[0].Messages != 2 || cs[0].LastAt == nil {
		t.Errorf("conversations: %+v %v", cs, err)
	}
}

// A removed member's messages keep their author.
func TestMessagesKeepARemovedAuthor(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.s.EnsureConversation(ctx, f.be98.ID, "x", "x", f.devCode.ID)
	f.s.AddMessage(ctx, c.ID, f.devCode.ID, "found it", nil)
	if err := f.s.DeleteIdentity(ctx, f.devCode.ID, audit("delete")); err != nil {
		t.Fatal(err)
	}
	ms, err := f.s.Messages(ctx, c.ID, 0, 10)
	if err != nil || len(ms) != 1 || ms[0].Author.Name != "dev-code" || !ms[0].Author.Deleted() {
		t.Errorf("after removing the author: %+v %v", ms, err)
	}
}

func TestLiveFilesAreCutWhenTheServerRestarts(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.s.EnsureConversation(ctx, f.be98.ID, "x", "x", f.devCode.ID)
	m, _ := f.s.AddMessage(ctx, c.ID, f.devCode.ID, "", []NewFile{{Name: "serial.log", ContentType: "text/plain"}})
	f.s.GrowFile(ctx, m.Files[0].ID, 1000)
	n, err := f.s.CutLiveFiles(ctx)
	if err != nil || n != 1 {
		t.Fatalf("cut: %d %v", n, err)
	}
	fw, _ := f.s.File(ctx, m.Files[0].ID)
	if fw.State != Cut || fw.Size != 1000 {
		t.Errorf("after cut: %+v", fw.File)
	}
	if err := f.s.StoreFile(ctx, fw.ID, "abc", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("a cut file was stored: %v", err)
	}
}

func TestDeletingAProjectTakesItsConversations(t *testing.T) {
	f := newFixture(t)
	c, _, _ := f.s.EnsureConversation(ctx, f.be98.ID, "x", "x", f.devCode.ID)
	f.s.AddMessage(ctx, c.ID, f.devCode.ID, "hi", []NewFile{{Name: "a", ContentType: "text/plain"}})
	if err := f.s.DeleteProject(ctx, f.be98.ID, audit("delete")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Conversation(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("conversation survived its project: %v", err)
	}
}
