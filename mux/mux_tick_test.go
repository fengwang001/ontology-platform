package mux

import (
	"errors"
	"testing"
	"time"
)

// Regression: Tick used to delete the id from the seen set when a
// waiter expired, so a response arriving after the timeout was
// miscounted as an orphan instead of late.
func TestTickKeepsIDSeenForLateAccounting(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.Now)
	done := make(chan error, 1)
	go func() {
		_, err := m.Wait("slow", clk.Now().Add(time.Second))
		done <- err
	}()
	waitPending(t, m, 1)
	clk.Advance(2 * time.Second)
	m.Tick()
	if err := <-done; !errors.Is(err, ErrTimedOut) {
		t.Fatalf("Wait err=%v, want ErrTimedOut", err)
	}
	m.Deliver("slow", []byte("too-late"))
	if s := m.Stats(); s.Late != 1 || s.Orphans != 0 {
		t.Fatalf("stats %+v, want Late=1 Orphans=0", s)
	}
	// The id completed, so it can be registered again.
	if _, err := m.Register("slow"); err != nil {
		t.Fatalf("re-register after timeout: %v", err)
	}
}
