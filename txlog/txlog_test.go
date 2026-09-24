package txlog

import "testing"

// TestCheckedCountConstant is white-box on purpose: the checked counter must
// stay unexported, so only an in-package test may read it; it never passes
// through an exported function/method.
//
// m producers append one record each (first offsets 0..m-1), HW covers them
// (that step is not counted). Committing the latest-first producer then
// advancing HW by 1, and committing the earliest-first producer and advancing
// HW by 1 again, must each inspect only a constant number of transactions.
func TestCheckedCountConstant(t *testing.T) {
	const maxCheck = 4
	for _, m := range []int{100, 1000, 10000} {
		l := New()
		for p := 1; p <= m; p++ {
			if _, err := l.AppendData(p, "x"); err != nil {
				t.Fatalf("m=%d append: %v", m, err)
			}
		}
		if err := l.AdvanceHW(m); err != nil { // not counted
			t.Fatalf("m=%d initial HW: %v", m, err)
		}

		if _, err := l.AppendCommit(m); err != nil { // latest first offset
			t.Fatalf("m=%d commit latest: %v", m, err)
		}
		if err := l.AdvanceHW(m + 1); err != nil {
			t.Fatalf("m=%d HW m+1: %v", m, err)
		}
		l.mu.Lock()
		c1, lso1 := l.checked, l.lso
		l.mu.Unlock()
		if c1 > maxCheck {
			t.Fatalf("m=%d latest commit inspected %d txns, want <= %d", m, c1, maxCheck)
		}
		if lso1 != 0 {
			t.Fatalf("m=%d LSO after latest commit = %d, want 0", m, lso1)
		}

		if _, err := l.AppendCommit(1); err != nil { // earliest first offset
			t.Fatalf("m=%d commit earliest: %v", m, err)
		}
		if err := l.AdvanceHW(m + 2); err != nil {
			t.Fatalf("m=%d HW m+2: %v", m, err)
		}
		l.mu.Lock()
		c2, lso2 := l.checked, l.lso
		l.mu.Unlock()
		if c2 > maxCheck {
			t.Fatalf("m=%d earliest commit inspected %d txns, want <= %d", m, c2, maxCheck)
		}
		if lso2 != 1 { // second producer by first offset now bounds the LSO
			t.Fatalf("m=%d LSO after earliest commit = %d, want 1", m, lso2)
		}
	}
}

// TestCheckedResetsOnReject ensures a rejected AdvanceHW neither changes the
// counter nor any state (invariant 4 at the txlog layer).
func TestCheckedResetsOnReject(t *testing.T) {
	l := New()
	l.AppendData(1, "a")
	l.AdvanceHW(1)
	if err := l.AdvanceHW(0); err != ErrHWOutOfRange {
		t.Fatalf("backward HW: got %v, want ErrHWOutOfRange", err)
	}
	if err := l.AdvanceHW(2); err != ErrHWOutOfRange {
		t.Fatalf("HW past end: got %v, want ErrHWOutOfRange", err)
	}
	if got := l.HW(); got != 1 {
		t.Fatalf("HW after rejected advances = %d, want 1", got)
	}
}
