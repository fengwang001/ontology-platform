package reasm

import (
	"testing"
	"time"
)

// TestEvictReleasesReceivedNotDeclared pins the contract that evicting an
// expired message frees exactly the bytes it actually occupied (received
// bytes), not its declared total. Releasing more than occupied erases other
// in-flight messages' usage and voids the global limit.
func TestEvictReleasesReceivedNotDeclared(t *testing.T) {
	clk := newClock()
	r := New(100, 60*time.Second, clk.Now)
	// "big" declares 50 bytes but only 5 ever arrive: it occupies 5.
	if _, _, err := r.Submit("big", 0, []byte("12345"), 50); err != nil {
		t.Fatal(err)
	}
	clk.Advance(30 * time.Second)
	// "other" receives 40 of its 41 declared bytes: it occupies 40.
	if _, _, err := r.Submit("other", 0, []byte("0123456789012345678901234567890123456789"), 41); err != nil {
		t.Fatal(err)
	}
	clk.Advance(31 * time.Second) // t=61s: "big" expired, "other" still alive
	if got := r.Status("big"); got != (Status{}) {
		t.Fatalf("Status(big) after expiry = %+v, want zero Status", got)
	}
	if got := r.Used(); got != 40 {
		t.Fatalf("used after eviction = %d, want 40 (only other's occupancy)", got)
	}
}
