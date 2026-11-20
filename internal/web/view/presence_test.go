package view

import (
	"testing"
	"time"
)

func TestPresenceCountsOpenFeedsAndRecentRequests(t *testing.T) {
	p := NewPresence()
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	recent, old := now.Add(-4*time.Minute), now.Add(-6*time.Minute)
	if p.Online("bob", nil, now) || !p.Online("bob", &recent, now) || p.Online("bob", &old, now) {
		t.Error("without a feed, online is a request within 5 minutes")
	}
	leave1, leave2 := p.Enter("bob"), p.Enter("bob")
	if !p.Online("bob", &old, now) || p.Live("alice") {
		t.Error("an open feed does not make bob online, or alice is")
	}
	leave1()
	leave1() // a second call ends nothing more
	if !p.Live("bob") {
		t.Error("bob went offline with a feed still open")
	}
	leave2()
	if p.Live("bob") {
		t.Error("bob is live with no feed open")
	}
	var none *Presence
	none.Enter("bob")()
	if none.Online("bob", nil, now) {
		t.Error("a nil Presence reports someone")
	}
}
