package pages

import (
	"context"
	"net/url"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/messaging"
)

// The members panel of a channel, Discord's member list: who can read it,
// among the principals the reader may know about, by kind. A column on
// wide screens, a sheet below; MembersPanel (src/components) refreshes it
// from GET /api/channels/{name}/members.

// MembersView is the panel (pages/chat.html).
type MembersView struct {
	Channel string // "lab/dut1", for the API
	Label   string // "#dut1"
	Groups  []MemberGroup
}

// MemberGroup is one kind of members; empty groups stay, hidden, for the
// bundle to fill.
type MemberGroup struct {
	Kind, Label string
	Members     []MemberView
}

// MemberView is partials/member.html (tpl-member).
type MemberView struct {
	Name, Initial, Kind string
	Href                string // the direct conversation, when the reader may open it
	Online, Following   bool
	Seen, Follows       string
}

// memberGroups are the panel's groups, in its order.
var memberGroups = []struct {
	kind  dto.PrincipalKind
	label string
}{
	{dto.KindHuman, "People"}, {dto.KindAgent, "Agents"}, {dto.KindDevice, "Devices"},
}

// MemberSeen is a member's presence line; src/render/format.ts
// memberSeen matches it.
func MemberSeen(m dto.Member, now time.Time) string {
	switch {
	case m.Online:
		return "online"
	case m.LastSeen == nil:
		return "never seen"
	}
	return "last seen " + MessageTime(m.LastSeen.Time, now)
}

// FollowsText says whether a member follows the channel; format.ts
// followsText matches it.
func FollowsText(following bool) string {
	if following {
		return "follows"
	}
	return "does not follow"
}

// Member is the row of a member.
func Member(m dto.Member, now time.Time) MemberView {
	v := MemberView{Name: m.Name, Initial: initial(m.Name), Kind: string(m.Kind), Online: m.Online, Following: m.Following,
		Seen: MemberSeen(m, now), Follows: FollowsText(m.Following)}
	if m.CanDM {
		v.Href = "/dm/" + url.PathEscape(m.Name)
	}
	return v
}

func (s *Site) membersView(ctx context.Context, p auth.Principal, ch messaging.Channel) (*MembersView, error) {
	list, err := s.chats.svc.ChannelMembers(ctx, p, ch.Name, s.presence.Online)
	if err != nil {
		return nil, err
	}
	now := s.d.Clock.Now()
	v := &MembersView{Channel: ch.Name, Label: "#" + ch.Local()}
	for _, g := range memberGroups {
		group := MemberGroup{Kind: string(g.kind), Label: g.label}
		for _, m := range list {
			if m.Kind == g.kind {
				group.Members = append(group.Members, Member(m, now))
			}
		}
		v.Groups = append(v.Groups, group)
	}
	return v, nil
}
