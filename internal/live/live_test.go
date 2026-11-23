package live

import "testing"

func TestSubscribersHearWhatTheyAccept(t *testing.T) {
	h := New()
	be98 := h.Subscribe(func(k string) bool { p, _ := Parse(k); return p.ProjectID == 1 })
	all := h.Subscribe(nil)
	h.Publish(ProjectKey(1), ConversationKey(2, 7))
	if n := <-be98.C; n.Key != "p:1" {
		t.Errorf("be98 got %+v", n)
	}
	if len(be98.C) != 0 {
		t.Error("be98 heard about project 2")
	}
	if len(all.C) != 2 {
		t.Errorf("an unfiltered subscriber heard %d notices", len(all.C))
	}
	h.Unsubscribe(all)
	h.Publish(ProjectKey(1))
	if len(all.C) != 2 {
		t.Error("an unsubscribed stream still hears")
	}
}

// A subscriber that falls behind is told to read everything again, and the
// writer never waits for it.
func TestFallingBehindMeansResync(t *testing.T) {
	h := New()
	s := h.Subscribe(nil)
	for i := 0; i < buffer+10; i++ {
		h.Publish(ProjectKey(1))
	}
	for len(s.C) > 0 {
		<-s.C
	}
	h.Publish(ProjectKey(1))
	if n := <-s.C; !n.Resync {
		t.Errorf("after losing notices: %+v, want Resync", n)
	}
	h.Publish(ProjectKey(1))
	if n := <-s.C; n.Resync || n.Key != "p:1" {
		t.Errorf("after the resync: %+v", n)
	}
}

func TestCloseEndsEveryStream(t *testing.T) {
	h := New()
	s := h.Subscribe(nil)
	h.Close()
	if _, open := <-s.C; open {
		t.Error("a stream stayed open after Close")
	}
	late := h.Subscribe(nil)
	if _, open := <-late.C; open {
		t.Error("a stream made after Close is open")
	}
	h.Publish(ProjectKey(1)) // must not panic on a closed channel
}

func TestANilHubIgnoresEverything(t *testing.T) {
	var h *Hub
	s := h.Subscribe(nil)
	h.Publish(ProjectKey(1))
	h.Unsubscribe(s)
	if len(s.C) != 0 {
		t.Error("a nil hub delivered")
	}
}

func TestParse(t *testing.T) {
	for key, want := range map[string]Parsed{
		"p:3":    {Kind: 'p', ProjectID: 3},
		"c:3:12": {Kind: 'c', ProjectID: 3, ConversationID: 12},
		"m:5":    {Kind: 'm', IdentityID: 5},
	} {
		if got, ok := Parse(key); !ok || got != want {
			t.Errorf("Parse(%q) = %+v %v", key, got, ok)
		}
	}
	for _, bad := range []string{"", "p", "p:x", "c:1", "x:1", "m:1:2"} {
		if _, ok := Parse(bad); ok {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}
