package mux

import (
	"testing"
	"time"
)

// Regression: Deliver used to hand the caller's slice straight to the
// waiter, so mutating the caller's buffer after Deliver corrupted the
// already-dispatched payload. The fix copies the slice at hand-off.
func TestDeliverCopiesPayload(t *testing.T) {
	m := New(newFakeClock().Now)
	done := make(chan []byte, 1)
	go func() {
		got, err := m.Wait("iso", time.Unix(1<<40, 0))
		if err != nil {
			t.Errorf("Wait: %v", err)
		}
		done <- got
	}()
	waitPending(t, m, 1)
	payload := []byte("payload")
	m.Deliver("iso", payload)
	for i := range payload {
		payload[i] = 'X'
	}
	if got := <-done; string(got) != "payload" {
		t.Fatalf("got %q, want %q (payload not isolated from caller)", got, "payload")
	}
}
