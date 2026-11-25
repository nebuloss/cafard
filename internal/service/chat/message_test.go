package chat

import (
	"strings"
	"testing"

	"cafard/internal/service/access"
	"cafard/internal/store"
)

// A message is its sender's to edit and delete, and nobody else's; deleted,
// it stays, empty, with its files gone.
func TestEditAndDeleteMessage(t *testing.T) {
	w := newWorld(t)
	sent, err := w.svc.SendText(ctx, w.guillaume, "be98", "wifi-drops", "the drop is at 30 s")
	if err != nil {
		t.Fatal(err)
	}
	id := sent.Message.ID
	if err := w.svc.EditMessage(ctx, w.devCode, id, "mine now"); !access.Is(err, access.NotAllowed) {
		t.Errorf("another member edits: %v", err)
	}
	if err := w.svc.DeleteMessage(ctx, w.devCode, id); !access.Is(err, access.NotAllowed) {
		t.Errorf("another member deletes: %v", err)
	}
	if err := w.svc.EditMessage(ctx, w.guillaume, id, "  "); !access.Is(err, access.BadValue) {
		t.Errorf("edited to nothing: %v", err)
	}
	if err := w.svc.EditMessage(ctx, w.guillaume, id, "the drop is at 32 s"); err != nil {
		t.Fatal(err)
	}
	ms := messages(t, w, sent.Conversation.ID)
	if ms[0].Text != "the drop is at 32 s" || ms[0].EditedAt == nil {
		t.Errorf("after the edit: %q %v", ms[0].Text, ms[0].EditedAt)
	}

	f, err := w.svc.SendFile(ctx, w.guillaume, "be98", "wifi-drops", "boot.log", "text/plain", "", strings.NewReader("U-Boot\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.svc.DeleteMessage(ctx, w.guillaume, f.Message.ID); err != nil {
		t.Fatal(err)
	}
	ms = messages(t, w, sent.Conversation.ID)
	gone := ms[len(ms)-1]
	if gone.DeletedAt == nil || gone.Text != "" || len(gone.Files) != 1 || gone.Files[0].State != store.Deleted {
		t.Errorf("after the delete: %+v", gone)
	}
	if err := w.svc.DeleteMessage(ctx, w.guillaume, f.Message.ID); err == nil {
		t.Error("deleted twice")
	}
	if err := w.svc.EditMessage(ctx, w.guillaume, f.Message.ID, "back"); err == nil {
		t.Error("a deleted message was edited")
	}
}

func messages(t *testing.T, w *world, conv int64) []store.Message {
	t.Helper()
	ms, err := w.st.LatestMessages(ctx, conv, 50)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}
