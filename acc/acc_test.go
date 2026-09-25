package acc

import "testing"

// TestProbeBound proves dedup checks a constant number of pending
// entries no matter how large pending grows: a map lookup, not a
// linear scan. probes is unexported; only this white-box test (same
// package) can read it, never through the public API.
func TestProbeBound(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		a := New(m + 1)
		for o := int64(0); o < int64(m); o++ {
			if err := a.Apply("k", o, 1); err != nil {
				t.Fatalf("m=%d fill o=%d: %v", m, o, err)
			}
		}
		if err := a.Apply("k", int64(m), 1); err != nil {
			t.Fatalf("m=%d new offset: %v", m, err)
		}
		if a.probes > 1 {
			t.Fatalf("m=%d: probes=%d, want <=1 (map dedup, not a scan)", m, a.probes)
		}
	}
}

// TestCommitContiguous checks cp advances only over the gap-free prefix.
func TestCommitContiguous(t *testing.T) {
	for _, tc := range []struct {
		offsets []int64
		wantCP  int64
		wantSum int64
	}{
		{[]int64{0, 1, 2}, 2, 6},
		{[]int64{0, 2, 3}, 0, 1},
		{[]int64{1, 2, 3}, -1, 0},
		{[]int64{2, 0, 1}, 2, 6}, // arrival order irrelevant
		{[]int64{0, 0, 1}, 1, 3}, // inflight dup counted once
	} {
		a := New(8)
		for _, o := range tc.offsets {
			if err := a.Apply("k", o, o+1); err != nil {
				t.Fatalf("offsets=%v apply o=%d: %v", tc.offsets, o, err)
			}
		}
		a.Commit()
		if a.Checkpoint() != tc.wantCP || a.Sum("k") != tc.wantSum {
			t.Fatalf("offsets=%v: cp=%d sum=%d, want cp=%d sum=%d",
				tc.offsets, a.Checkpoint(), a.Sum("k"), tc.wantCP, tc.wantSum)
		}
	}
}

// TestRestoreDropsPendingOnly: sum and cp survive, pending is lost.
func TestRestoreDropsPendingOnly(t *testing.T) {
	a := New(8)
	_ = a.Apply("k", 0, 5)
	_ = a.Apply("k", 2, 7)
	a.Commit()
	a.Restore()
	if a.Checkpoint() != 0 || a.Sum("k") != 5 || len(a.Pending()) != 0 {
		t.Fatalf("cp=%d sum=%d pending=%v", a.Checkpoint(), a.Sum("k"), a.Pending())
	}
}
