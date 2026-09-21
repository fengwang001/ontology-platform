package mux

import (
	"errors"
	"testing"
	"time"
)

// Regression: Tick used to delete the expired id from the seen set,
// erasing its registration history, so a response arriving after the
// timeout was miscounted as an orphan instead of late. seen must keep
// every id ever registered for the life of the Mux.
func TestLateAfterTimeoutNotOrphan(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.Now)
	done := make(chan error, 1)
	go func() {
		_, err := m.Wait("tardy", clk.Now().Add(time.Second))
		done <- err
	}()
	waitPending(t, m, 1)
	clk.Advance(2 * time.Second)
	m.Tick()
	if err := <-done; !errors.Is(err, ErrTimedOut) {
		t.Fatalf("Wait err=%v, want ErrTimedOut", err)
	}
	m.Deliver("tardy", []byte("too-slow"))
	s := m.Stats()
	if s.Late != 1 || s.Orphans != 0 {
		t.Fatalf("stats %+v, want Late=1 Orphans=0", s)
	}
}
