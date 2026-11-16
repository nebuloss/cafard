package hub

import (
	"regexp"
	"testing"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/store"
)

func TestFilterMatch(t *testing.T) {
	alice := auth.Principal{Name: "alice"}
	dmVisible := func(p auth.Principal, e store.Entry) bool { return e.Stream == "dm:alice~bob" && p.Name == "alice" }
	line := store.Entry{Stream: "serial:dut1", Kind: store.KindLine, Severity: 6, Text: "login: "}
	errLine := store.Entry{Stream: "syslog:dut1-a", Kind: store.KindLine, Severity: 3, Text: "oops"}
	dm := store.Entry{Stream: "dm:alice~bob", Kind: store.KindMessage, Severity: 6, Text: "hi"}
	other := store.Entry{Stream: "dm:bob~carol", Kind: store.KindMessage, Severity: 6, Text: "hi"}
	cases := []struct {
		name string
		f    Filter
		e    store.Entry
		want bool
	}{
		{"all logs", Filter{MinSeverity: 7}, line, true},
		{"messages not in all", Filter{MinSeverity: 7}, dm, false},
		{"glob", Filter{Streams: []string{"syslog:dut1*"}, MinSeverity: 7}, errLine, true},
		{"glob misses", Filter{Streams: []string{"syslog:dut2*"}, MinSeverity: 7}, errLine, false},
		{"star never matches messages", Filter{Streams: []string{"*"}, MinSeverity: 7}, dm, false},
		{"severity", Filter{MinSeverity: 3}, line, false},
		{"severity keeps errors", Filter{MinSeverity: 3}, errLine, true},
		{"kinds", Filter{Kinds: []store.Kind{store.KindMarker}, MinSeverity: 7}, line, false},
		{"pattern", Filter{Pattern: regexp.MustCompile("login:"), MinSeverity: 7}, line, true},
		{"viewer sees own dm", Filter{Streams: []string{"dm:*"}, MinSeverity: 7, Viewer: &alice}, dm, true},
		{"viewer never sees others' dm", Filter{Streams: []string{"dm:*"}, MinSeverity: 7, Viewer: &alice}, other, false},
	}
	for _, c := range cases {
		if got := c.f.Match(c.e, dmVisible); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestDefaultVisibilityHidesMessagesFromViewers(t *testing.T) {
	h := New(nil)
	alice := auth.Principal{Name: "alice"}
	sub := h.Subscribe(Filter{Streams: []string{"chan:*"}, MinSeverity: 7, Viewer: &alice}, 4)
	defer sub.Close()
	h.Publish([]store.Entry{{Seq: 1, Stream: "chan:general", Kind: store.KindMessage, Text: "x"}})
	if len(sub.C()) != 0 {
		t.Error("message delivered with no visibility rule installed")
	}
	h.SetVisible(func(auth.Principal, store.Entry) bool { return true })
	h.Publish([]store.Entry{{Seq: 2, Stream: "chan:general", Kind: store.KindMessage, Text: "x"}})
	if len(sub.C()) != 1 {
		t.Error("message not delivered once visible")
	}
}

func TestCloseUnsubscribes(t *testing.T) {
	h := New(nil)
	sub := h.Subscribe(Filter{MinSeverity: 7}, 1)
	sub.Close()
	h.Publish([]store.Entry{{Seq: 1, Stream: "push:a"}})
	if h.Subscribers() != 0 || len(sub.C()) != 0 {
		t.Error("closed subscription still receives")
	}
}
