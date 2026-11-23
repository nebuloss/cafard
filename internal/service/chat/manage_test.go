package chat

import (
	"strings"
	"testing"

	"cafard/internal/service/access"
	"cafard/internal/store"
)

func TestSlugAndTitle(t *testing.T) {
	for title, want := range map[string]string{
		"Wi-Fi drops after the 2nd reboot":     "wifi-drops-after-the-2nd-reboot",
		"Le débit chute à 2 Mbit/s après 30 s": "le-debit-chute-a-2-mbit-s-apres-30-s",
		"  ???  ":                         "conversation",
		"Signed OTA image for rc3!":       "signed-ota-image-for-rc3",
		strings.Repeat("abcdefghij ", 10): "abcdefghij-abcdefghij-abcdefghij-abcdefghij",
	} {
		if got := Slug(title); got != want {
			t.Errorf("Slug(%q) = %q, want %q", title, got, want)
		}
	}
	if got := TitleFrom("LED blinks orange after the second reboot and Wi-Fi is gone."); got != "LED blinks orange after the second reboot" {
		t.Errorf("TitleFrom = %q", got)
	}
	if got := TitleFrom("Done."); got != "Done" {
		t.Errorf("TitleFrom = %q", got)
	}
}

func TestStartConversationFromItsFirstMessage(t *testing.T) {
	w := newWorld(t)
	a, err := w.svc.StartConversation(ctx, w.guillaume, "be98", "", "Upload drops to 2 Mbit/s after 30 s. @dev-code can you look?")
	if err != nil {
		t.Fatal(err)
	}
	if a.Conversation.Title != "Upload drops to 2 Mbit/s after 30" || a.Conversation.Name != "upload-drops-to-2-mbit-s-after-30" || a.Message.ID == 0 {
		t.Errorf("first: %+v", a.Conversation)
	}
	b, err := w.svc.StartConversation(ctx, w.guillaume, "be98", "", "Upload drops to 2 Mbit/s after 30 s, again")
	if err != nil || b.Conversation.Name != "upload-drops-to-2-mbit-s-after-30-2" {
		t.Errorf("the same title twice: %+v %v", b.Conversation, err)
	}
	if _, err := w.svc.StartConversation(ctx, w.labBench, "be98", "", "hello"); !access.Is(err, access.NotMember) {
		t.Errorf("a non-member: %v", err)
	}
	if _, err := w.svc.StartConversation(ctx, w.guillaume, "be98", "", "   "); !access.Is(err, access.BadValue) {
		t.Errorf("nothing to start with: %v", err)
	}
}

// A caption and three photos are one message.
func TestAttachFilesToTheMessageJustSent(t *testing.T) {
	w := newWorld(t)
	sent, _ := w.svc.StartConversation(ctx, w.guillaume, "be98", "", "Photos of the board")
	c := sent.Conversation.Name
	for _, name := range []string{"IMG_1.jpg", "IMG_2.jpg"} {
		if _, err := w.svc.AttachFile(ctx, w.guillaume, "be98", c, sent.Message.ID, name, "", strings.NewReader("\xff\xd8\xff photo")); err != nil {
			t.Fatal(err)
		}
	}
	_, ms, _ := w.svc.Messages(ctx, w.devCode, "be98", c, 0, 0)
	if len(ms) != 1 || len(ms[0].Files) != 2 || ms[0].Text != "Photos of the board" || ms[0].Files[0].ContentType != "image/jpeg" {
		t.Fatalf("messages: %+v", ms)
	}
	if _, err := w.svc.AttachFile(ctx, w.devCode, "be98", c, sent.Message.ID, "x.log", "", strings.NewReader("x")); !access.Is(err, access.NotAllowed) {
		t.Errorf("attaching to someone else's message: %v", err)
	}
}

func TestReadsAndUnread(t *testing.T) {
	w := newWorld(t)
	sent, _ := w.svc.StartConversation(ctx, w.guillaume, "be98", "", "LED blinks orange")
	c := sent.Conversation
	w.svc.SendText(ctx, w.guillaume, "be98", c.Name, "and Wi-Fi is gone")

	unread, _ := w.svc.Unread(ctx, w.devCode, "be98")
	if unread[c.ID] != 2 {
		t.Errorf("dev-code unread before reading: %v", unread)
	}
	if u, _ := w.svc.Unread(ctx, w.guillaume, "be98"); u[c.ID] != 0 {
		t.Errorf("guillaume's own messages counted as unread: %v", u)
	}
	w.svc.Messages(ctx, w.devCode, "be98", c.Name, 0, 0)
	if u, _ := w.svc.Unread(ctx, w.devCode, "be98"); u[c.ID] != 0 {
		t.Errorf("dev-code unread after reading: %v", u)
	}
	reads, _ := w.svc.Reads(ctx, w.guillaume, "be98", c.Name)
	var devCodeRead int64
	for _, r := range reads {
		if r.Member.Name == "dev-code" {
			devCodeRead = r.MessageID
		}
	}
	if devCodeRead == 0 {
		t.Errorf("reads: %+v", reads)
	}
	// Reading an earlier page does not move the position back.
	w.svc.Messages(ctx, w.devCode, "be98", c.Name, 0, 1)
	reads, _ = w.svc.Reads(ctx, w.guillaume, "be98", c.Name)
	for _, r := range reads {
		if r.Member.Name == "dev-code" && r.MessageID != devCodeRead {
			t.Errorf("the read position went back: %d, was %d", r.MessageID, devCodeRead)
		}
	}
}

func TestRenameAndClose(t *testing.T) {
	w := newWorld(t)
	sent, _ := w.svc.StartConversation(ctx, w.guillaume, "be98", "", "first")
	title, closed := "Wi-Fi drops after second reboot", true
	c, err := w.svc.UpdateConversation(ctx, w.devCode, "be98", sent.Conversation.Name, &title, &closed)
	if err != nil || c.Title != title || c.ClosedAt == nil {
		t.Errorf("update: %+v %v", c, err)
	}
	empty := ""
	if _, err := w.svc.UpdateConversation(ctx, w.guillaume, "be98", sent.Conversation.Name, &empty, nil); !access.Is(err, access.BadValue) {
		t.Errorf("an empty title: %v", err)
	}
}

func TestDeleteFile(t *testing.T) {
	w := newWorld(t)
	a, _ := w.svc.SendFile(ctx, w.device, "be98", "x", "secret.log", "", "", strings.NewReader("password=hunter2\n"))
	b, _ := w.svc.SendFile(ctx, w.device, "be98", "y", "again.log", "", "", strings.NewReader("password=hunter2\n"))
	fa, fb := a.Message.Files[0], b.Message.Files[0]

	if err := w.svc.DeleteFile(ctx, w.devCode, fa.ID); !access.Is(err, access.NotAllowed) {
		t.Errorf("a member who did not send it: %v", err)
	}
	if err := w.svc.DeleteFile(ctx, w.device, fa.ID); err != nil {
		t.Fatalf("the sender: %v", err)
	}
	if _, _, err := w.svc.File(ctx, w.devCode, fa.ID); !access.Is(err, Gone) {
		t.Errorf("the deleted file: %v", err)
	}
	// The same bytes are still attached to the other message: kept.
	if !w.svc.bl.Has(fb.SHA256) {
		t.Fatal("the blob went while another message still holds it")
	}
	if err := w.svc.DeleteFile(ctx, w.guillaume, fb.ID); err != nil {
		t.Fatalf("an admin: %v", err)
	}
	if w.svc.bl.Has(fb.SHA256) {
		t.Error("the blob stayed after its last file was deleted")
	}
	if err := w.svc.DeleteFile(ctx, w.guillaume, fb.ID); !access.Is(err, Gone) {
		t.Errorf("deleting twice: %v", err)
	}
	_, ms, _ := w.svc.Messages(ctx, w.devCode, "be98", "y", 0, 0)
	if len(ms) != 1 || ms[0].Files[0].State != store.Deleted {
		t.Errorf("the message after: %+v", ms)
	}
}

func TestMembersFromTheChat(t *testing.T) {
	w := newWorld(t)
	if err := w.svc.AddMember(ctx, w.guillaume, "be98", "lab-bench"); err != nil {
		t.Fatal(err)
	}
	ms, _ := w.svc.Members(ctx, w.guillaume, "be98")
	found := false
	for _, m := range ms {
		found = found || m.Name == "lab-bench"
	}
	if !found {
		t.Errorf("members: %+v", ms)
	}
	if err := w.svc.AddMember(ctx, w.devCode, "zeeos", "lab-bench"); !access.Is(err, access.NotAllowed) {
		t.Errorf("an agent adding a member: %v", err)
	}
	dir, err := w.svc.Directory(ctx, w.guillaume)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range dir {
		if i.Kind == store.Device {
			t.Errorf("a device in the directory: %s", i.Name)
		}
	}
	if _, err := w.svc.Directory(ctx, w.devCode); !access.Is(err, access.NotAllowed) {
		t.Errorf("an agent reading the directory: %v", err)
	}
}
