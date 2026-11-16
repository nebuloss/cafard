// Package testutil holds small helpers shared by tests.
package testutil

import (
	"testing"
	"time"
)

// Eventually polls cond until it holds, and fails the test with what
// once timeout passes.
func Eventually(t testing.TB, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
