package escrow

import "testing"

// TestTouchedIndependentOfOtherSessions proves that one Offer's lock
// replacement reads/writes at most (old entries + new entries + 2)
// session-lock records, no matter how many other sessions the player
// has: 1 vs 1000 other sessions must produce the identical count.
func TestTouchedIndependentOfOtherSessions(t *testing.T) {
	for _, others := range []int{1, 1000} {
		e := New()
		for i := 0; i < others; i++ {
			e.Replace(int64(1000+i), "p", map[string]int64{"z": 1}, 5)
		}
		// Old quote in the target session: 3 item entries + gold.
		e.Replace(1, "p", map[string]int64{"a": 1, "b": 2, "c": 3}, 10)
		e.touched = 0
		// New quote: 2 item entries + gold. Expect 3+2+2 records.
		e.Replace(1, "p", map[string]int64{"d": 4, "e": 5}, 7)
		if want := 3 + 2 + 2; e.touched != want {
			t.Fatalf("others=%d: touched = %d, want %d", others, e.touched, want)
		}
		// Replacing with an empty quote: 2+0+2 records.
		e.touched = 0
		e.Replace(1, "p", nil, 0)
		if want := 2 + 0 + 2; e.touched != want {
			t.Fatalf("others=%d: touched = %d, want %d", others, e.touched, want)
		}
	}
}

// TestAggregates checks the per-player locked totals across sessions,
// replacement and release (expiry materialization touches are separate
// and not bounded by the Offer bound).
func TestAggregates(t *testing.T) {
	e := New()
	e.Replace(1, "p", map[string]int64{"a": 2, "b": 1}, 10)
	e.Replace(2, "p", map[string]int64{"a": 3}, 5)
	e.Replace(3, "q", map[string]int64{"a": 7}, 0)
	if got := e.LockedQty("p", "a"); got != 5 {
		t.Fatalf("p locked a = %d, want 5", got)
	}
	if got := e.LockedQty("p", "b"); got != 1 {
		t.Fatalf("p locked b = %d, want 1", got)
	}
	if got := e.LockedGold("p"); got != 15 {
		t.Fatalf("p locked gold = %d, want 15", got)
	}
	// Replacement moves the totals, not just adds.
	old := e.Replace(1, "p", map[string]int64{"c": 4}, 0)
	if len(old.Items) != 2 || old.Gold != 10 {
		t.Fatalf("old = %+v, want 2 items and 10 gold", old)
	}
	if got := e.LockedQty("p", "a"); got != 3 {
		t.Fatalf("p locked a = %d, want 3", got)
	}
	if got := e.LockedQty("p", "c"); got != 4 {
		t.Fatalf("p locked c = %d, want 4", got)
	}
	if got := e.LockedGold("p"); got != 5 {
		t.Fatalf("p locked gold = %d, want 5", got)
	}
	// Release drops everything of the session.
	e.Release(2)
	if got := e.LockedQty("p", "a"); got != 0 {
		t.Fatalf("p locked a = %d, want 0", got)
	}
	if got := e.LockedGold("p"); got != 0 {
		t.Fatalf("p locked gold = %d, want 0", got)
	}
	if got := e.LockedQty("q", "a"); got != 7 {
		t.Fatalf("q locked a = %d, want 7 (untouched)", got)
	}
	e.Release(999) // no-op
}
