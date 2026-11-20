package messaging

import (
	"slices"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/projects"
)

// memberNames are the members' names, in the panel's order.
func memberNames(ms []dto.Member) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

// The members panel lists who can read a channel, and only principals
// the viewer may know about: nobody from a project the viewer has no
// grant in, nobody without read on the channel, and nothing at all to a
// viewer who cannot read it.
func TestChannelMembersStayInsideSharedProjects(t *testing.T) {
	e := newEnv(t)
	if _, err := e.d.Projects.Create(e.ctx, "rack", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tokens.EnsurePrincipal(e.ctx, auth.AdminName, auth.KindHuman); err != nil {
		t.Fatal(err)
	}
	alice := e.human("alice")
	builder := e.agent("builder")
	if _, err := e.svc.CreateChannel(e.ctx, builder, dto.ChannelCreate{Name: "dut1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetFollow(e.ctx, builder, "dut1", true); err != nil {
		t.Fatal(err)
	}
	// Reads one channel of lab only: a member of #dut1, not of #general.
	pinned := e.outsider("pinned-agent")
	e.grant(projects.Grant{Principal: "pinned-agent", Project: "lab", Channel: "dut1", Scopes: auth.NewScopeSet(auth.ScopeRead)})
	// In lab, but on another channel, or without read: not members.
	e.outsider("other-chan")
	e.grant(projects.Grant{Principal: "other-chan", Project: "lab", Channel: "general", Scopes: auth.NewScopeSet(auth.ScopeRead)})
	e.outsider("ingest-only")
	e.grant(projects.Grant{Principal: "ingest-only", Project: "lab", Scopes: auth.NewScopeSet(auth.ScopeIngest)})
	// Only in rack: lab's members never name it, and it sees no lab channel.
	rackOnly := e.outsider("rack-only")
	e.grant(projects.Grant{Principal: "rack-only", Project: "rack", Scopes: auth.NewScopeSet(auth.ScopeRead, auth.ScopeMessage)})
	// A device in rack and lab is a member and known to lab's readers.
	if _, err := e.tokens.EnsurePrincipal(e.ctx, "bench-pi", auth.KindDevice); err != nil {
		t.Fatal(err)
	}
	e.grant(projects.Grant{Principal: "bench-pi", Project: "lab", Scopes: auth.NewScopeSet(auth.ScopeRead)})

	online := func(name string, _ *time.Time, _ time.Time) bool { return name == "builder" }
	want := []string{"admin", "alice", "bench-pi", "builder", "pinned-agent"}
	for _, viewer := range []auth.Principal{alice, builder, pinned, e.admin()} {
		ms, err := e.svc.ChannelMembers(e.ctx, viewer, "lab/dut1", online)
		if err != nil {
			t.Fatalf("%s: %v", viewer.Name, err)
		}
		if got := memberNames(ms); !slices.Equal(slices.Sorted(slices.Values(got)), want) {
			t.Errorf("%s sees members %v, want %v", viewer.Name, got, want)
		}
		for _, m := range ms {
			if m.CanDM == (m.Name == viewer.Name) {
				t.Errorf("%s: can_dm %s = %v", viewer.Name, m.Name, m.CanDM)
			}
		}
	}

	ms, err := e.svc.ChannelMembers(e.ctx, pinned, "lab/dut1", online)
	if err != nil {
		t.Fatal(err)
	}
	// People, agents, devices; online first, then by name.
	if got := memberNames(ms); !slices.Equal(got, []string{"admin", "alice", "builder", "pinned-agent", "bench-pi"}) {
		t.Errorf("the panel's order = %v", got)
	}
	byName := map[string]dto.Member{}
	for _, m := range ms {
		byName[m.Name] = m
	}
	if b := byName["builder"]; !b.Online || !b.Following || b.Kind != dto.KindAgent {
		t.Errorf("builder = %+v, want an online agent who follows", b)
	}
	if a := byName["alice"]; a.Online || !a.Following || a.Kind != dto.KindHuman {
		t.Errorf("alice = %+v, want an offline human who follows (people follow by default)", a)
	}
	if p := byName["pinned-agent"]; p.Following || !p.Online {
		t.Errorf("pinned-agent = %+v, want the viewer online, not following #dut1 without asking", p)
	}

	// No grant on the channel: unknown, like any other channel call.
	for _, viewer := range []auth.Principal{rackOnly, e.outsider("nobody")} {
		if ms, err := e.svc.ChannelMembers(e.ctx, viewer, "lab/dut1", online); code(err) != dto.CodeUnknownChannel || len(ms) != 0 {
			t.Errorf("%s: members %v, err %v", viewer.Name, memberNames(ms), err)
		}
	}
	// rack's channel lists rack's readers to rack-only, and nobody of lab
	// who has no grant there.
	ms, err = e.svc.ChannelMembers(e.ctx, rackOnly, "rack/general", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := memberNames(ms); !slices.Equal(got, []string{"admin", "rack-only"}) {
		t.Errorf("rack/general members = %v", got)
	}
}
