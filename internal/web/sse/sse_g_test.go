package sse_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
	"github.com/nebuloss/cafard/internal/store"
	"github.com/nebuloss/cafard/internal/web/webtest"
)

func TestStreamStatusCarriesTheLastLine(t *testing.T) {
	e, srv := newServer(t, webtest.Options{}, nil)
	tok := e.Principal("alice", auth.KindHuman, auth.ScopeRead)
	c, _ := connect(t, srv, "/api/events?scope=status", tok)
	c.next(t, "counts")
	e.Commit(webtest.Line("serial:dut1", "\x1b[1mU-Boot\x1b[0m 2024.04"))
	deadline := time.After(5 * time.Second)
	for {
		var st dto.Stream
		_ = json.Unmarshal([]byte(c.next(t, "stream_status").Data), &st)
		if st.ID == "serial:dut1" && st.LastLine == "U-Boot 2024.04" && st.LastSeq > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("stream_status never carried the last line: %+v", st)
		default:
		}
	}
}

func TestLiveMessagesFollowTheChannel(t *testing.T) {
	visible := func(p auth.Principal, e store.Entry) bool { return fakeMessaging{}.Visible(context.Background(), p, e) }
	e, srv := newServer(t, webtest.Options{Visible: visible, Messaging: fakeMessaging{}}, nil)
	tok := e.Principal("carol", auth.KindHuman, auth.ScopeRead)
	c, _ := connect(t, srv, "/api/events?channel=dut1&kinds=line,message", tok)
	c.next(t, "counts")
	e.Commit(
		store.Entry{Stream: "chan:general", Kind: store.KindMessage, Severity: 6, Text: "elsewhere", Sender: "codex"},
		store.Entry{Stream: "dm:alice~carol", Kind: store.KindMessage, Severity: 6, Text: "a direct one", Sender: "alice"},
		store.Entry{Stream: "chan:dut1", Kind: store.KindMessage, Severity: 6, Text: "in dut1", Sender: "codex"},
		webtest.Line("serial:dut1", "bound line"),
	)
	if got := entryOf(t, c.next(t, "entry")); got.Text != "in dut1" {
		t.Errorf("first entry %q, want the channel's message", got.Text)
	}
	if got := entryOf(t, c.next(t, "entry")); got.Text != "bound line" {
		t.Errorf("second entry %q", got.Text)
	}
	c.none(t, 50*time.Millisecond)
}
