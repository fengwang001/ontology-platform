package reasm

import (
	"testing"
	"time"
)

// TestEvictionReleasesOnlyOccupiedBytes pins the contract that evicting an
// expired message releases exactly the bytes it occupied (received bytes),
// not its declared total; over-releasing would erase the occupancy of other
// in-flight messages and void the limit.
func TestEvictionReleasesOnlyOccupiedBytes(t *testing.T) {
	clk := newClock()
	r := New(100, time.Minute, clk.Now)

	// "a" declares 50 bytes but only 5 ever arrive: it occupies 5.
	if _, _, err := r.Submit("a", 0, []byte("12345"), 50); err != nil {
		t.Fatal(err)
	}
	clk.Advance(30 * time.Second)
	// "b" occupies 40 received bytes of a declared 80 and stays in flight.
	if _, _, err := r.Submit("b", 0, []byte("0123456789012345678901234567890123456789"), 80); err != nil {
		t.Fatal(err)
	}
	clk.Advance(31 * time.Second) // "a" is now past its deadline, "b" is not

	// Trigger the lazy eviction of "a" without touching "b".
	if st := r.Status("a"); st != (Status{}) {
		t.Fatalf("expired message still visible: %+v", st)
	}
	if got := r.Used(); got != 40 {
		t.Fatalf("Used = %d after evicting a 5-byte message, want 40", got)
	}
	if st := r.Status("b"); st.Received != 40 {
		t.Fatalf("b lost state after a's eviction: %+v", st)
	}
}

// TestUsedLazilyExpiresMessages pins the contract that read-only queries
// report values as judged at the current clock: a message past its TTL must
// not count toward the global occupancy even if nothing else has touched it.
func TestUsedLazilyExpiresMessages(t *testing.T) {
	clk := newClock()
	r := New(100, time.Minute, clk.Now)
	if _, _, err := r.Submit("a", 0, []byte("12345"), 50); err != nil {
		t.Fatal(err)
	}
	if got := r.Used(); got != 5 {
		t.Fatalf("Used = %d, want 5", got)
	}
	clk.Advance(2 * time.Minute)
	if got := r.Used(); got != 0 {
		t.Fatalf("Used = %d after TTL elapsed, want 0", got)
	}
	// A repeated query at the same instant must agree with itself.
	if got := r.Used(); got != 0 {
		t.Fatalf("second Used = %d, want 0", got)
	}
	if st := r.Status("a"); st != (Status{}) {
		t.Fatalf("expired message still visible: %+v", st)
	}
}
