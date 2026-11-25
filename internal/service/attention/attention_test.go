package attention

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"cafard/internal/blobs"
	"cafard/internal/live"
	"cafard/internal/service/access"
	"cafard/internal/service/chat"
	"cafard/internal/store"
)

var ctx = context.Background()

type world struct {
	st                         *store.Store
	chat                       *chat.Service
	att                        *Service
	be98                       store.Project
	guillaume, devCode, device access.Who
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "cafard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bl, err := blobs.Open(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	acc := access.New(st)
	g, err := acc.CreateIdentity(ctx, access.Admin("test"), access.NewIdentity{Kind: store.Person, Name: "guillaume"})
	if err != nil {
		t.Fatal(err)
	}
	hub := live.New()
	t.Cleanup(hub.Close)
	st.Announce(hub)
	w := &world{st: st, chat: chat.New(st, bl, acc), att: New(st, acc, hub), guillaume: access.Who{Identity: g, Origin: "test"}}
	mk := func(name string, kind store.Kind) access.Who {
		i, err := acc.CreateIdentity(ctx, access.Admin("test"), access.NewIdentity{Kind: kind, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return access.Who{Identity: i, Origin: "test"}
	}
	w.devCode, w.device = mk("dev-code", store.Agent), mk("dev-build", store.Device)
	if w.be98, err = acc.CreateProject(ctx, access.Admin("test"), "be98", 30, 5e9); err != nil {
		t.Fatal(err)
	}
	for _, m := range []access.Who{w.guillaume, w.devCode, w.device} {
		if err := acc.AddMember(ctx, access.Admin("test"), w.be98, m.Identity); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func (w *world) send(t *testing.T, who access.Who, conv, text string) {
	t.Helper()
	if _, err := w.chat.SendText(ctx, who, "be98", conv, text); err != nil {
		t.Fatal(err)
	}
}

func names(es []Entry) string {
	var out []string
	for _, e := range es {
		out = append(out, e.Conversation.Name+":"+string(e.Reason))
	}
	return strings.Join(out, " ")
}

// The rule: an agent is offered what mentions it and what it follows, and
// nothing else that is new in the project.
func TestOnlyWhatIsAddressedToTheAgent(t *testing.T) {
	w := newWorld(t)
	w.send(t, w.guillaume, "chatter", "lunch at noon?")
	w.send(t, w.guillaume, "wifi-drops", "@dev-code the upload drops after 30 s")
	w.send(t, w.device, "build", "build failed @dev-code")
	w.send(t, w.guillaume, "emails", "write to guillaume@dev-code.lan")

	in, err := w.att.Inbox(ctx, w.devCode, w.be98)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(in); got != "build:mentioned wifi-drops:mentioned" {
		t.Errorf("inbox: %s", got)
	}
	if in[0].By.AuthorName != "dev-build" || in[0].By.AuthorKind != store.Device {
		t.Errorf("mentioned by %+v", in[0].By)
	}

	// Reading it takes it off; posting in it follows it.
	if _, _, err := w.chat.MessagesPage(ctx, w.devCode, "be98", "wifi-drops", chat.Page{}); err != nil {
		t.Fatal(err)
	}
	w.send(t, w.devCode, "wifi-drops", "looking")
	in, _ = w.att.Inbox(ctx, w.devCode, w.be98)
	if got := names(in); got != "build:mentioned" {
		t.Errorf("after reading: %s", got)
	}
	w.send(t, w.guillaume, "wifi-drops", "here is the second run")
	in, _ = w.att.Inbox(ctx, w.devCode, w.be98)
	if got := names(in); got != "build:mentioned wifi-drops:following" {
		t.Errorf("after a reply: %s", got)
	}

	// Closing takes it out of attention.
	closed := true
	if _, err := w.chat.UpdateConversation(ctx, w.guillaume, "be98", "wifi-drops", nil, &closed); err != nil {
		t.Fatal(err)
	}
	in, _ = w.att.Inbox(ctx, w.devCode, w.be98)
	if got := names(in); got != "build:mentioned" {
		t.Errorf("after closing: %s", got)
	}
}

func TestADeviceHasNoInbox(t *testing.T) {
	w := newWorld(t)
	if _, err := w.att.Inbox(ctx, w.device, w.be98); !access.Is(err, access.SendOnly) {
		t.Errorf("a device's inbox: %v", err)
	}
}

func TestMention(t *testing.T) {
	m := Mention("dev-code")
	for text, want := range map[string]bool{
		"@dev-code":             true,
		"ping @dev-code, thx":   true,
		"@Dev-Code look":        true,
		"done @dev-code.":       true,
		"(@dev-code)":           true,
		"@dev-code-2 look":      false,
		"me@dev-code.lan":       false,
		"dev-code without at":   false,
		"see @dev-code.lan now": false,
	} {
		if got := m.MatchString(text); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}
