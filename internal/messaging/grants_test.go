package messaging

import (
	"slices"
	"strings"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/projects"
	"github.com/nebuloss/cafard/internal/store"
)

// outsider is a principal with no grant at all.
func (e *env) outsider(name string) auth.Principal {
	e.t.Helper()
	if _, err := e.tokens.EnsurePrincipal(e.ctx, name, auth.KindAgent); err != nil {
		e.t.Fatal(err)
	}
	return auth.Principal{Name: name, Kind: auth.KindAgent, Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage), TokenID: 1}
}

func (e *env) grant(g projects.Grant) {
	e.t.Helper()
	if err := e.d.Projects.SetGrant(e.ctx, g); err != nil {
		e.t.Fatal(err)
	}
}

func TestNoGrantSeesNothing(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "dut1", Patterns: []string{"serial:dut1"}}); err != nil {
		t.Fatal(err)
	}
	line := e.line("serial:lab/dut1", "boot", 6)
	msg := e.send(a, text("hello everyone"))
	q := e.send(a, SendInput{SendRequest: sendReq("press reset", dto.RecipientHumans, "")})
	o := e.outsider("nobody")

	if chs, err := e.svc.Channels(e.ctx, o, true); err != nil || len(chs) != 0 {
		t.Errorf("channels = %v %v", chs, err)
	}
	if chats, err := e.svc.Chats(e.ctx, o, ChatsQuery{All: true}); err != nil || len(chats) != 0 {
		t.Errorf("chats = %+v %v", chats, err)
	}
	if got := inbox(t, e, o); len(got) != 0 {
		t.Errorf("inbox = %v", got)
	}
	for _, seq := range []int64{msg.Seq, q.Seq} {
		if _, err := e.svc.Message(e.ctx, o, seq); code(err) != dto.CodeNotFound {
			t.Errorf("message %d: %v", seq, err)
		}
	}
	if _, err := e.svc.Timeline(e.ctx, o, "chan:lab/general", TimelineQuery{}); code(err) != dto.CodeNotFound {
		t.Errorf("timeline: %v", err)
	}
	if e.svc.Visible(e.ctx, o, store.Entry{Seq: line, Stream: "serial:lab/dut1"}) {
		t.Error("a log line is visible without a grant")
	}
	if _, err := e.svc.SendMessage(e.ctx, o, text("let me in")); code(err) != dto.CodeUnknownChannel {
		t.Errorf("send: %v", err)
	}
	if _, err := e.svc.SendMessage(e.ctx, o, SendInput{SendRequest: sendReq("psst", "agent-a", "")}); code(err) != dto.CodeNotFound {
		t.Errorf("a direct message to someone sharing no project: %v", err)
	}
	if _, err := e.svc.CreateChannel(e.ctx, o, dto.ChannelCreate{Name: "mine"}); code(err) != dto.CodeForbidden {
		t.Errorf("create: %v", err)
	}
	if _, _, err := e.svc.Claim(e.ctx, o, "serial:lab/dut1", 0, ""); code(err) != dto.CodeNotFound {
		t.Errorf("claim: %v", err)
	}
	if c, err := e.svc.Counts(e.ctx, o); err != nil || len(c.Chats) != 0 || c.OpenQuestions != 0 {
		t.Errorf("counts = %+v %v", c, err)
	}
	ps, err := e.svc.Projects(e.ctx, o)
	if err != nil || len(ps) != 0 {
		t.Errorf("projects = %+v %v", ps, err)
	}
}

func TestChannelGrantSeesOnlyThatChannel(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	for _, name := range []string{"dut1", "dut2"} {
		if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: name, Patterns: []string{"serial:" + name}}); err != nil {
			t.Fatal(err)
		}
	}
	e.line("serial:lab/dut1", "dut1 boot", 6)
	e.line("serial:lab/dut2", "dut2 boot", 6)
	in1 := e.send(a, SendInput{SendRequest: sendReq("on dut1", "", "dut1")})
	e.send(a, SendInput{SendRequest: sendReq("on dut2", "", "dut2")})
	e.send(a, text("in general"))

	c := e.outsider("narrow")
	e.grant(projects.Grant{Principal: "narrow", Project: "lab", Channel: "dut1", Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage)})
	chs, err := e.svc.Channels(e.ctx, c, true)
	if err != nil || len(chs) != 1 || chs[0].Name != "lab/dut1" {
		t.Fatalf("channels = %+v %v", chs, err)
	}
	if _, err := e.svc.Message(e.ctx, c, in1.Seq); err != nil {
		t.Errorf("its channel's message: %v", err)
	}
	if !e.svc.Visible(e.ctx, c, store.Entry{Stream: "serial:lab/dut1"}) || e.svc.Visible(e.ctx, c, store.Entry{Stream: "serial:lab/dut2"}) {
		t.Error("the bound stream is not the only one visible")
	}
	if _, pats, err := e.svc.ResolveChannel(e.ctx, c, "dut1"); err != nil || !slices.Equal(pats, []string{"serial:lab/dut1"}) {
		t.Errorf("resolve = %v %v", pats, err)
	}
	if _, _, err := e.svc.ResolveChannel(e.ctx, c, "dut2"); code(err) != dto.CodeUnknownChannel {
		t.Errorf("another channel: %v", err)
	}
	// A channel grant cannot widen itself or make channels.
	if _, err := e.svc.UpdateChannel(e.ctx, c, "dut1", dto.ChannelUpdate{AddPatterns: []string{"serial:dut2"}}); code(err) != dto.CodeForbidden {
		t.Errorf("binding with a channel grant: %v", err)
	}
	if _, err := e.svc.CreateChannel(e.ctx, c, dto.ChannelCreate{Name: "more", Project: "lab"}); code(err) != dto.CodeForbidden {
		t.Errorf("creating with a channel grant: %v", err)
	}
	if _, err := e.svc.SendMessage(e.ctx, c, text("in general?")); code(err) != dto.CodeUnknownChannel {
		t.Errorf("posting in general: %v", err)
	}
	if _, err := e.svc.SendMessage(e.ctx, c, SendInput{SendRequest: sendReq("on dut1 too", "", "dut1")}); err != nil {
		t.Errorf("posting in its channel: %v", err)
	}
	// Bindings stay inside their project.
	if _, err := e.svc.UpdateChannel(e.ctx, a, "dut1", dto.ChannelUpdate{AddPatterns: []string{"serial:other/dut1"}}); code(err) != dto.CodeInvalidPattern {
		t.Errorf("binding another project: %v", err)
	}
	// A read-only token is capped below its grant.
	ro := c
	ro.Scopes = auth.NewScopeSet(auth.ScopeRead)
	if _, err := e.svc.SendMessage(e.ctx, ro, SendInput{SendRequest: sendReq("x", "", "dut1")}); code(err) != dto.CodeForbidden {
		t.Errorf("a read-only token posted: %v", err)
	}
	ps, err := e.svc.Projects(e.ctx, c)
	if err != nil || len(ps) != 1 || len(ps[0].Scopes) != 0 || !slices.Equal(ps[0].Channels, []string{"chan:lab/dut1"}) || len(ps[0].Sinks) != 0 {
		t.Errorf("projects = %+v %v", ps, err)
	}
}

func TestLogAttachmentHidesLinesOfStreamsTheReaderCannotRead(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "dut1", Patterns: []string{"serial:dut1"}}); err != nil {
		t.Fatal(err)
	}
	first := e.line("serial:lab/secret", "the password is hunter2", 6)
	m := e.send(a, SendInput{SendRequest: dto.SendRequest{Text: "look", Channel: "dut1",
		Logs: []dto.LogRequest{{Stream: "serial:lab/secret", FromSeq: first, ToSeq: ptr(first)}}}})
	snap := m.Attachments[0].SnapshotSHA256
	if snap == "" {
		t.Fatal("no snapshot")
	}
	c := e.outsider("narrow")
	e.grant(projects.Grant{Principal: "narrow", Project: "lab", Channel: "dut1", Scopes: auth.NewScopeSet(auth.ScopeRead)})
	d, err := e.svc.EntryDTO(e.ctx, c, m)
	if err != nil {
		t.Fatal(err)
	}
	la := d.Attachments[0].Log
	if la == nil || !la.Hidden || len(la.Preview) != 0 || la.Snapshot || la.Lines != 0 {
		t.Errorf("attachment for the channel-only reader = %+v", la)
	}
	if _, _, err := e.svc.AttachmentLines(e.ctx, c, m.Seq, 0, 0, 10); code(err) != dto.CodeNotFound {
		t.Errorf("attachment lines: %v", err)
	}
	if ok, _ := e.svc.Blobs().VisibleTo(e.ctx, snap, c); ok {
		t.Error("the snapshot blob is visible to a reader who cannot read its stream")
	}
	if ok, _ := e.svc.Blobs().VisibleTo(e.ctx, snap, a); !ok {
		t.Error("the snapshot blob is hidden from a project member")
	}
	page, _, err := e.svc.AttachmentLines(e.ctx, a, m.Seq, 0, 0, 10)
	if err != nil || len(page.Entries) != 1 || !strings.Contains(page.Entries[0].Text, "hunter2") {
		t.Errorf("member's lines = %+v %v", page, err)
	}
	// The sender must be able to read what it attaches.
	_, err = e.svc.SendMessage(e.ctx, c, SendInput{SendRequest: dto.SendRequest{Text: "x", Channel: "dut1",
		Logs: []dto.LogRequest{{Stream: "serial:lab/secret", FromSeq: first}}}})
	if code(err) != dto.CodeForbidden && code(err) != dto.CodeNotFound {
		t.Errorf("attaching an unreadable stream: %v", err)
	}
}

func TestBareChannelNamesResolveAcrossProjects(t *testing.T) {
	e := newEnv(t)
	if _, err := e.d.Projects.Create(e.ctx, "other", ""); err != nil {
		t.Fatal(err)
	}
	a := e.agent("agent-a")
	e.grant(projects.Grant{Principal: "agent-a", Project: "other", Scopes: auth.NewScopeSet(auth.ScopeRead)})
	if _, err := e.svc.Channel(e.ctx, a, "general"); code(err) != dto.CodeUnknownChannel || !strings.Contains(err.Error(), "lab/general, other/general") {
		t.Errorf("ambiguous general: %v", err)
	}
	// Sending picks the one a may post in.
	m, err := e.svc.SendMessage(e.ctx, a, text("hi"))
	if err != nil || m.Stream != "chan:lab/general" {
		t.Errorf("send = %+v %v", m, err)
	}
	if _, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "x", Project: "other"}); code(err) != dto.CodeForbidden {
		t.Errorf("creating where a reads only: %v", err)
	}
	ch, err := e.svc.CreateChannel(e.ctx, a, dto.ChannelCreate{Name: "x"})
	if err != nil || ch.Name != "lab/x" {
		t.Errorf("create = %+v %v", ch, err)
	}
}

func TestDirectMessagesNeedASharedProject(t *testing.T) {
	e := newEnv(t)
	a := e.agent("agent-a")
	b := e.agent("agent-b")
	e.outsider("loner")
	if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: sendReq("hi b", "agent-b", "")}); err != nil {
		t.Errorf("between members: %v", err)
	}
	if _, err := e.svc.SendMessage(e.ctx, a, SendInput{SendRequest: sendReq("hi", "loner", "")}); code(err) != dto.CodeNotFound {
		t.Errorf("to a loner: %v", err)
	}
	if _, err := e.tokens.EnsurePrincipal(e.ctx, auth.AdminName, auth.KindHuman); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SendMessage(e.ctx, b, SendInput{SendRequest: sendReq("help", auth.AdminName, "")}); err != nil {
		t.Errorf("to the admin: %v", err)
	}
	rows, err := e.tokens.Principals(e.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	reach, err := e.d.Projects.Reachable(e.ctx, a, rows)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range reach {
		names = append(names, r.Name)
	}
	if !slices.Equal(names, []string{"admin", "agent-b"}) {
		t.Errorf("reachable = %v", names)
	}
}
