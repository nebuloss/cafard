package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	l := New(3, time.Minute)
	start := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return start }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("10.0.50.44"); !ok {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	ok, wait := l.Allow("10.0.50.44")
	if ok || wait != time.Minute {
		t.Errorf("fourth attempt: ok %v, wait %v", ok, wait)
	}
	if ok, _ := l.Allow("10.0.50.45"); !ok {
		t.Error("another key was refused")
	}
	l.now = func() time.Time { return start.Add(61 * time.Second) }
	if ok, _ := l.Allow("10.0.50.44"); !ok {
		t.Error("refused after the window")
	}
	if len(l.seen) != 1 {
		t.Errorf("old buckets kept: %d", len(l.seen))
	}
}
