package reasm

import (
	"testing"
	"time"
)

// TestUsedAppliesLazyExpiry pins the contract that the read-only global
// occupancy query reports the true value as of the current clock: a message
// past its TTL must not be counted by Used even if nothing else has touched
// it. Two consecutive queries at the same instant must agree.
func TestUsedAppliesLazyExpiry(t *testing.T) {
	clk := newClock()
	r := New(100, time.Minute, clk.Now)
	if _, _, err := r.Submit("m", 0, []byte("12345"), 10); err != nil {
		t.Fatal(err)
	}
	if got := r.Used(); got != 5 {
		t.Fatalf("used before expiry = %d, want 5", got)
	}
	clk.Advance(2 * time.Minute) // "m" is expired; nothing else touches it
	if got := r.Used(); got != 0 {
		t.Fatalf("used after expiry = %d, want 0", got)
	}
	if got := r.Used(); got != 0 {
		t.Fatalf("second used query = %d, want 0 (queries must be stable)", got)
	}
	if got := r.Status("m"); got != (Status{}) {
		t.Fatalf("Status(m) after expiry = %+v, want zero Status", got)
	}
}
