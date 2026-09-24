package assign

import (
	"strconv"
	"testing"
)

// TestBoundedChecked pins the complexity constraint: after m members each
// hold exactly one partition, a single Leave must touch only a constant
// number of partitions (plus this batch's migrations and the leaver's
// former holdings), never growing linearly with m.
func TestBoundedChecked(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(m)
		ids := make([]string, m)
		for i := range ids {
			ids[i] = strconv.Itoa(i)
		}
		if mig := s.Rebalance(ids); mig != 0 {
			t.Fatalf("m=%d: initial join migrated %d", m, mig)
		}
		leaverHeld := len(s.held[ids[0]])
		mig := s.Rebalance(ids[1:])
		if leaverHeld != 1 || mig != 1 {
			t.Fatalf("m=%d: leaverHeld=%d mig=%d, want 1/1", m, leaverHeld, mig)
		}
		if s.checked > mig+leaverHeld+4 {
			t.Errorf("m=%d: checked=%d exceeds constant bound", m, s.checked)
		}
	}
}
