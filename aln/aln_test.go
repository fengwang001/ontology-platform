package aln

import "testing"

// TestCmpLinear proves the running min is maintained incrementally: feeding
// m strictly decreasing events into one batch costs exactly m comparisons
// (one per event), not ~m*(m-1)/2 (a full rescan per event).
func TestCmpLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		var a Aligner
		a.Begin()
		for i := 0; i < m; i++ {
			a.Accept(int64(m - i)) // strictly decreasing TS
		}
		if a.cmp != m {
			t.Fatalf("m=%d: cmp=%d, want %d (quadratic would be %d)", m, a.cmp, m, m*(m-1)/2)
		}
		a.Close()
		if got := a.Times()[0]; got != 1 {
			t.Fatalf("m=%d: aligned=%d, want 1", m, got)
		}
	}
}

// TestCloseSemantics pins lateness boundary, carry-over and the None case.
func TestCloseSemantics(t *testing.T) {
	var a Aligner
	a.Begin()
	a.Close() // batch 0: no accepted events -> None, still no lower bound
	if got := a.Times(); len(got) != 1 || got[0] != None {
		t.Fatalf("empty batch 0: times=%v, want [None]", got)
	}
	if a.Late(-1 << 62) {
		t.Fatal("nothing is late before any aligned time exists")
	}
	a.Begin()
	a.Accept(7)
	a.Accept(9)
	a.Close()
	a.Begin()
	a.Close() // batch 2: empty -> carries A(1)=7
	if got := a.Times(); got[1] != 7 || got[2] != 7 {
		t.Fatalf("times=%v, want [None 7 7]", got)
	}
	if !a.Late(6) || a.Late(7) { // strict: TS == prev aligned is on time
		t.Fatal("late boundary must be strict less-than")
	}
}
