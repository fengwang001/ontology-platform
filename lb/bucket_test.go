package lb

import "testing"

// TestDrainChecksBounded proves Drain advances via the head pointer rather
// than scanning the whole queue: with m items all still in the system, the
// first probe finds the head has not departed and Drain stops immediately,
// so the probe count stays at a small constant independent of m.
func TestDrainChecksBounded(t *testing.T) {
	// Several orders of magnitude of m; the bound must not move.
	cases := []int{100, 1000, 5000, 10000}
	for _, m := range cases {
		b := New(int64(m)+1, 1)
		for i := 0; i < m; i++ {
			if _, full := b.Admit(int64(i) * 1000); full {
				t.Fatalf("m=%d: unexpected full at i=%d", m, i)
			}
		}
		if b.InSystem() != m {
			t.Fatalf("m=%d: in-system=%d want %d", m, b.InSystem(), m)
		}
		b.Drain(0) // earliest departure is 1, so nothing has left by t=0
		if got := b.drainCheckCount(); got > drainProbeBound {
			t.Fatalf("m=%d: drain probed %d entries, want <= %d (no linear scan)",
				m, got, drainProbeBound)
		}
		if b.InSystem() != m {
			t.Fatalf("m=%d: in-system changed to %d after a drain that removes nothing", m, b.InSystem())
		}
	}
}

// TestDrainAndAdmit replays the canonical eight-step scenario directly at
// the core level: one Drain then one Admit per arrival. Each row gives the
// expected removals, admission result, departure and post-step count.
func TestDrainAndAdmit(t *testing.T) {
	type row struct {
		t        int64
		removed  int
		admit    bool
		dep      int64
		inSystem int
	}
	rows := []row{
		{0, 0, true, 5, 1},
		{5, 1, true, 10, 1}, // dep=5 <= 5 leaves
		{6, 0, true, 15, 2},
		{10, 1, true, 20, 2}, // dep=10 <= 10 leaves (<= boundary)
		{15, 1, true, 25, 2}, // dep=15 <= 15 leaves
		{15, 0, true, 30, 3},
		{15, 0, false, 0, 3}, // full: rejected, state untouched
		{21, 1, true, 35, 3}, // dep=20 <= 21 leaves
	}
	b := New(3, 5)
	for i, r := range rows {
		before := b.InSystem()
		b.Drain(r.t)
		removed := before - b.InSystem()
		if removed != r.removed {
			t.Fatalf("step %d t=%d: removed %d want %d", i, r.t, removed, r.removed)
		}
		dep, full := b.Admit(r.t)
		if full == r.admit || (!full && dep != r.dep) {
			t.Fatalf("step %d t=%d: got (dep=%d full=%v) want (dep=%d admit=%v)",
				i, r.t, dep, full, r.dep, r.admit)
		}
		if b.InSystem() != r.inSystem {
			t.Fatalf("step %d t=%d: in-system=%d want %d", i, r.t, b.InSystem(), r.inSystem)
		}
	}
}
