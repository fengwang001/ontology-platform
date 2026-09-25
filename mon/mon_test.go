package mon

import (
	"math/rand"
	"testing"

	"ontology/sla"
)

// TestSlidingWindow pins the exact ring-buffer semantics: the seven-step
// NOTES sequence, then random sequences checked against a naive suffix
// recompute (the definition: violations in the last w completions >= k).
func TestSlidingWindow(t *testing.T) {
	lats := []int64{10, 15, 5, 11, 12, 4, 20}
	wantCount := []int{0, 1, 1, 2, 3, 2, 3}
	wantBr := []bool{false, false, false, false, true, false, true}
	m := NewMonitor(10, 4, 3)
	for i, l := range lats {
		m.Begin()
		m.Complete(l)
		if m.win.violations != wantCount[i] {
			t.Fatalf("step %d: window violations=%d want %d", i+1, m.win.violations, wantCount[i])
		}
		if m.Breached() != wantBr[i] {
			t.Fatalf("step %d: breached=%v want %v", i+1, m.Breached(), wantBr[i])
		}
	}

	rng := rand.New(rand.NewSource(7))
	for caseN := 0; caseN < 200; caseN++ {
		w, k := 1+rng.Intn(8), 0
		threshold := int64(5)
		n := rng.Intn(30)
		got := NewMonitor(threshold, w, 1)
		got.win.k = 1 + rng.Intn(w)
		k = got.win.k
		done := []int64{}
		for i := 0; i < n; i++ {
			l := int64(rng.Intn(12))
			got.Begin()
			got.Complete(l)
			done = append(done, l)
			s, e := len(done)-w, 0
			if s < 0 {
				s = 0
			}
			for _, l := range done[s:] {
				if sla.Classify(l, threshold) == sla.Violation {
					e++
				}
			}
			if got.Breached() != (e >= k) {
				t.Fatalf("case %d step %d: breached=%v want %v>=%d", caseN, i, got.Breached(), e, k)
			}
		}
	}
}

// TestWindowUpdateConstant proves O(1) updates: after m completions the
// number of records inspected for the next End is a fixed small constant,
// independent of m (ring buffer + running counter, never a rescan).
func TestWindowUpdateConstant(t *testing.T) {
	tiers := []int{100, 500, 1000, 5000, 10000}
	seen := -1
	for _, m := range tiers {
		mn := NewMonitor(10, 4, 3)
		for i := 0; i < m; i++ {
			mn.Begin()
			mn.Complete(int64(i % 20))
		}
		mn.Begin()
		mn.Complete(5)
		got := mn.win.lastChecks
		if got > 2 {
			t.Fatalf("m=%d: inspected %d records, want constant <= 2", m, got)
		}
		if seen == -1 {
			seen = got
		} else if got != seen {
			t.Fatalf("inspected %d at m=%d, previously %d: must not grow with m", got, m, seen)
		}
	}
}
