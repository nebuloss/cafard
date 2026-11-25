// Package attention decides what an agent is shown: what is addressed to it,
// never "something new in the project" (docs/design.md, "Attention"; rule 2
// in CLAUDE.md). It is the only package that decides this; the MCP tools
// show what it returns and choose nothing themselves.
//
// Access says what an agent may read; attention says what it should. An
// agent in a busy project may read everything there and is offered only:
//
//   - conversations where a message it has not been shown @mentions it, by a
//     person, an agent or a device ("build failed @dev-code" calls it the way
//     a person would);
//   - conversations it has posted in, which it follows, when something new
//     has arrived there.
//
// A closed conversation leaves everyone's attention. What has been shown is
// the member's read position, the one the chat shows as "Read by dev-code":
// reading a conversation is what takes it off the inbox.
//
// Per-session attention, claims and follows of their own, comes with claims
// (design, "Sessions"); until then an agent's sessions share its read
// positions, as they share its access.
//
// An entry is a line, not the messages: why it is there, how much is new,
// and what reading it would cost a context, so the agent can choose.
package attention

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cafard/internal/live"
	"cafard/internal/service/access"
	"cafard/internal/store"
)

type Service struct {
	st  *store.Store
	acc *access.Service
	// hub is where Wait hears that a project changed; nil hears nothing,
	// and a wait then lasts its whole timeout.
	hub *live.Hub
}

func New(st *store.Store, acc *access.Service, hub *live.Hub) *Service {
	return &Service{st: st, acc: acc, hub: hub}
}

// Reason is why a conversation is in the inbox.
type Reason string

const (
	Mentioned Reason = "mentioned"
	Following Reason = "following"
)

// Entry is one conversation waiting for the agent.
type Entry struct {
	Conversation store.Conversation
	Reason       Reason
	// By is who mentioned the agent last, for Mentioned; who wrote last,
	// for Following.
	By     store.Unseen
	New    int
	Files  int
	Images int
	// Tokens is about what reading the new messages costs, images fitted
	// (internal/imagefit) included; logs are read by page and not counted.
	Tokens int
	// Latest is the newest unseen message's id: Wait tells by it what
	// arrived during a call.
	Latest int64
}

const (
	// scan is how many unseen messages are looked at, newest first: an
	// agent away for a long time still gets its mentions, and the count of
	// the rest says "200+".
	scan = 2000
	// imageTokens is a fitted photo's cost: (1568 × 1176) / 750 (imagefit).
	imageTokens = 2500
	// perMessage is what the wrapping of one message costs beside its text.
	perMessage = 30
)

// Inbox is what is waiting for who in p, mentions first, then the most
// recent.
func (s *Service) Inbox(ctx context.Context, who access.Who, p store.Project) ([]Entry, error) {
	if err := s.acc.CanRead(ctx, who, p); err != nil {
		return nil, err
	}
	unseen, err := s.st.Unseen(ctx, who.ID, p.ID, scan)
	if err != nil || len(unseen) == 0 {
		return nil, err
	}
	posted, err := s.st.PostedIn(ctx, who.ID, p.ID)
	if err != nil {
		return nil, err
	}
	mention := Mention(who.Name)
	byConv := map[int64]*Entry{}
	var order []int64
	for _, u := range unseen {
		e := byConv[u.ConversationID]
		if e == nil {
			e = &Entry{}
			byConv[u.ConversationID] = e
			order = append(order, u.ConversationID)
		}
		e.New++
		e.Latest = max(e.Latest, u.ID)
		e.Files += u.Files - u.Images
		e.Images += u.Images
		e.Tokens += perMessage + utf8.RuneCountInString(u.Text)/4 + u.Images*imageTokens
		if mention.MatchString(u.Text) {
			e.Reason, e.By = Mentioned, u
		} else if e.Reason != Mentioned {
			e.By = u
		}
	}
	convs, err := s.st.ConversationsByID(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, id := range order {
		e := byConv[id]
		if e.Reason == "" {
			if !posted[id] {
				continue
			}
			e.Reason = Following
		}
		c, ok := convs[id]
		if !ok || c.ClosedAt != nil {
			continue
		}
		e.Conversation = c
		out = append(out, *e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Reason == Mentioned) != (out[j].Reason == Mentioned) {
			return out[i].Reason == Mentioned
		}
		return out[i].By.CreatedAt.After(out[j].By.CreatedAt)
	})
	return out, nil
}

// Mention matches @name as a word: @dev-code in "ping @dev-code," and not in
// "@dev-code-2" or "me@dev-code.lan".
func Mention(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:^|[^\w@.-])@` + regexp.QuoteMeta(name) + `(?:$|[^\w@.-]|\.(?:$|\s))`)
}

// Quote is the start of a message's text for an inbox line: one line, at
// most n runes.
func Quote(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	r := []rune(text)
	return strings.TrimSpace(string(r[:n])) + "…"
}

// Since is how long ago t was, roughly, for a line a model reads.
func Since(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + " min ago"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + " h ago"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + " days ago"
}
