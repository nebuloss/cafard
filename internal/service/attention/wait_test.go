package attention

import (
	"testing"
	"time"
)

// A wait is woken by what arrives during it, addressed to the agent; what
// was already waiting comes beside it, and nothing addressed to it wakes it.
func TestWaitWakesOnWhatArrives(t *testing.T) {
	w := newWorld(t)
	w.send(t, w.guillaume, "old-drop", "@dev-code still there")

	type result struct {
		fresh, waiting []Entry
		err            error
		took           time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		f, wt, err := w.att.Wait(ctx, w.devCode, w.be98, 10*time.Second)
		done <- result{f, wt, err, time.Since(start)}
	}()

	// Something not for the agent: no wake.
	time.Sleep(200 * time.Millisecond)
	w.send(t, w.guillaume, "lunch", "who wants pizza")
	select {
	case r := <-done:
		t.Fatalf("woken by a message for nobody: %s", names(r.fresh))
	case <-time.After(800 * time.Millisecond):
	}

	// A mention: woken, at once.
	w.send(t, w.device, "build-42", "build failed @dev-code")
	select {
	case r := <-done:
		if r.err != nil || names(r.fresh) != "build-42:mentioned" || names(r.waiting) != "old-drop:mentioned" {
			t.Errorf("fresh %q, waiting %q, %v", names(r.fresh), names(r.waiting), r.err)
		}
		if r.took > 3*time.Second {
			t.Errorf("woken after %v", r.took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not woken by a mention")
	}
}

// With nothing new, a wait ends at its timeout with what is still waiting.
func TestWaitTimesOut(t *testing.T) {
	w := newWorld(t)
	w.send(t, w.guillaume, "old-drop", "@dev-code still there")
	start := time.Now()
	fresh, waiting, err := w.att.Wait(ctx, w.devCode, w.be98, 300*time.Millisecond)
	if err != nil || len(fresh) != 0 || names(waiting) != "old-drop:mentioned" {
		t.Errorf("fresh %q, waiting %q, %v", names(fresh), names(waiting), err)
	}
	if took := time.Since(start); took < 300*time.Millisecond || took > 2*time.Second {
		t.Errorf("took %v", took)
	}
}
