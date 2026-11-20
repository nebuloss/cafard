package messaging

import (
	"slices"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/mcpserver"
	"github.com/nebuloss/cafard/internal/projects"
)

func chatIDs(cs []dto.Chat) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

// A private channel is read by its creator and its members only: not by
// the rest of the project they share, not by another project's
// principals, not by the admin session; and adding a member reveals no one
// the creator could not reach.
func TestPrivateChannelsLeakToNoOne(t *testing.T) {
	e := newEnv(t)
	if _, err := e.d.Projects.Create(e.ctx, "rack", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tokens.EnsurePrincipal(e.ctx, auth.AdminName, auth.KindHuman); err != nil {
		t.Fatal(err)
	}
	alice := e.human("alice")
	bob := e.agent("bob")
	carol := e.human("carol") // in lab too, not added
	rackOnly := e.outsider("rack-only")
	e.grant(projects.Grant{Principal: "rack-only", Project: "rack", Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage)})
	admin := e.admin()

	// Members must be reachable; an unknown name is the same answer, and
	// nothing is made.
	for _, m := range []string{"rack-only", "ghost"} {
		_, err := e.svc.CreateChannel(e.ctx, alice, dto.ChannelCreate{Name: "notes", Private: true, Members: []string{"bob", m}})
		if code(err) != dto.CodeNotFound {
			t.Errorf("adding %s: %v", m, err)
		}
	}
	if _, err := e.svc.Channel(e.ctx, alice, "~alice/notes"); code(err) != dto.CodeUnknownChannel {
		t.Errorf("a refused private channel exists: %v", err)
	}
	for _, bad := range []dto.ChannelCreate{
		{Name: "notes", Private: true, Patterns: []string{"serial:lab/dut1"}},
		{Name: "notes", Private: true, Project: "lab"},
		{Name: "~bob/notes", Private: true},
		{Name: "notes", Members: []string{"bob"}},
	} {
		if _, err := e.svc.CreateChannel(e.ctx, alice, bad); code(err) != dto.CodeBadRequest {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	ch, err := e.svc.CreateChannel(e.ctx, alice, dto.ChannelCreate{Name: "notes", Title: "Our notes", Private: true, Members: []string{"bob", "alice"}})
	if err != nil || ch.Name != "~alice/notes" {
		t.Fatalf("create = %+v %v", ch, err)
	}
	msg := e.send(alice, SendInput{SendRequest: dto.SendRequest{Text: "only for bob", Channel: "~alice/notes"}})
	reply := e.send(bob, SendInput{SendRequest: dto.SendRequest{Text: "seen, alice", Channel: "~alice/notes"}})

	// The members read, post, list and count it; bob follows it although
	// agents follow nothing by default.
	for _, p := range []auth.Principal{alice, bob} {
		chats, err := e.svc.Chats(e.ctx, p, ChatsQuery{All: true})
		if err != nil || !slices.Contains(chatIDs(chats), "chan:~alice/notes") {
			t.Errorf("%s: chats %v %v", p.Name, chatIDs(chats), err)
		}
		if !e.svc.Visible(e.ctx, p, msg) || !e.svc.Visible(e.ctx, p, reply) {
			t.Errorf("%s cannot see the messages", p.Name)
		}
	}
	if fs, err := e.svc.Follow(e.ctx, bob, "chan:~alice/notes"); err != nil || !fs.Followed {
		t.Errorf("bob does not follow it: %+v %v", fs, err)
	}
	if c, err := e.svc.Counts(e.ctx, bob); err != nil || !slices.ContainsFunc(c.Chats, func(x dto.ChatCount) bool { return x.ID == "chan:~alice/notes" && x.Unread == 1 }) {
		t.Errorf("bob's counts = %+v %v", c, err)
	}
	ms, err := e.svc.ChannelMembers(e.ctx, bob, "~alice/notes", nil)
	if names := memberNames(ms); err != nil || !slices.Equal(slices.Sorted(slices.Values(names)), []string{"alice", "bob"}) {
		t.Errorf("members = %v %v", names, err)
	}

	// Everyone else, the admin session included, knows nothing of it.
	for _, p := range []auth.Principal{carol, rackOnly, admin} {
		if _, err := e.svc.Channel(e.ctx, p, "~alice/notes"); code(err) != dto.CodeUnknownChannel {
			t.Errorf("%s: channel %v", p.Name, err)
		}
		chats, err := e.svc.Chats(e.ctx, p, ChatsQuery{All: true, Archived: true})
		if err != nil || slices.Contains(chatIDs(chats), "chan:~alice/notes") {
			t.Errorf("%s: chats %v %v", p.Name, chatIDs(chats), err)
		}
		if e.svc.Visible(e.ctx, p, msg) {
			t.Errorf("%s sees a private message", p.Name)
		}
		if _, err := e.svc.Timeline(e.ctx, p, "chan:~alice/notes", TimelineQuery{}); err == nil {
			t.Errorf("%s reads the timeline", p.Name)
		}
		if _, err := e.svc.Message(e.ctx, p, msg.Seq); code(err) != dto.CodeNotFound {
			t.Errorf("%s: message %v", p.Name, err)
		}
		if _, err := e.svc.ChannelMembers(e.ctx, p, "~alice/notes", nil); code(err) != dto.CodeUnknownChannel {
			t.Errorf("%s: members %v", p.Name, err)
		}
		if _, err := e.svc.SendMessage(e.ctx, p, SendInput{SendRequest: dto.SendRequest{Text: "hi", Channel: "~alice/notes"}}); err == nil {
			t.Errorf("%s posts in it", p.Name)
		}
		if c, err := e.svc.Counts(e.ctx, p); err != nil || slices.ContainsFunc(c.Chats, func(x dto.ChatCount) bool { return x.ID == "chan:~alice/notes" }) {
			t.Errorf("%s's counts name it: %+v %v", p.Name, c, err)
		}
	}
	if got := inbox(t, e, admin); slices.Contains(got, msg.Seq) {
		t.Error("the admin's inbox holds a private message")
	}

	// A personal space is never listed as a project, to its owner either.
	for _, p := range []auth.Principal{alice, bob, admin} {
		list, err := e.svc.Projects(e.ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, pr := range list {
			if projects.Personal(pr.Name) {
				t.Errorf("%s lists %s", p.Name, pr.Name)
			}
		}
	}
	// It does not get in the way of a project: alice still makes channels
	// in lab without naming it, and it has no #general.
	if ch, err := e.svc.CreateChannel(e.ctx, alice, dto.ChannelCreate{Name: "run-42"}); err != nil || ch.Name != "lab/run-42" {
		t.Errorf("create without a project = %+v %v", ch, err)
	}
	if _, err := e.svc.Channel(e.ctx, alice, "~alice/general"); code(err) != dto.CodeUnknownChannel {
		t.Errorf("a personal space has a #general: %v", err)
	}
	// Nobody grants a personal space by hand.
	if _, err := projects.ParseGrants("~alice:notes=read"); err == nil {
		t.Error("a grant spec reaches a personal space")
	}
}

// Agents make and use private channels through MCP, a gallery as well.
func TestAgentsMakePrivateChannelsOverMCP(t *testing.T) {
	e := newEnv(t)
	bob := e.agent("bob")
	alice := e.human("alice")
	text, out, err := e.svc.toolCreateChannel(e.ctx, &mcpserver.Call{Principal: bob},
		createChannelIn{Name: "shots", Kind: dto.ChannelGallery, Private: true, Members: []string{"alice"}})
	if err != nil || out.Channel.ID != "chan:~bob/shots" || out.Channel.ChannelKind != dto.ChannelGallery {
		t.Fatalf("create_channel = %q %+v %v", text, out, err)
	}
	chs, err := e.svc.Channels(e.ctx, alice, false)
	if err != nil || !slices.ContainsFunc(chs, func(c Channel) bool { return c.Name == "~bob/shots" && c.Gallery() }) {
		t.Errorf("alice's channels lack bob's gallery: %v", err)
	}
	carol := e.human("carol")
	if chs, _ := e.svc.Channels(e.ctx, carol, true); slices.ContainsFunc(chs, func(c Channel) bool { return projects.Personal(c.Project()) }) {
		t.Error("carol lists a private channel")
	}
}
