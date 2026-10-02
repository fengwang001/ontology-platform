package splitdeque

import (
	"testing"
)

// TestReclaimHalf verifies g = ceil(|S|/2) when Pop hits an empty P.
func TestReclaimHalf(t *testing.T) {
	for _, n := range []int64{1, 2, 3, 4, 5, 7} {
		d := mustNew(t, 64, 64, 1, 100)
		for x := int64(1); x <= n; x++ {
			if err := d.Push(x); err != nil {
				t.Fatal(err)
			}
		}
		// Miss, then push one more: Rv=1 keeps the new element private
		// while the n old elements move into S.
		steal(t, d, 64)
		if err := d.Push(n + 1); err != nil {
			t.Fatal(err)
		}
		if d.s-d.t != n {
			t.Fatalf("n=%d: |S| = %d, want %d", n, d.s-d.t, n)
		}
		// Pop the private element, then P is empty with |S|=n: reclaim.
		if x, ok := d.Pop(); !ok || x != n+1 {
			t.Fatalf("n=%d: pop private = (%d,%v), want %d,true", n, x, ok, n+1)
		}
		before := d.s
		x, ok := d.Pop()
		if !ok {
			t.Fatalf("n=%d: reclaim pop empty", n)
		}
		g := before - d.s
		wantG := (n + 1) / 2
		if g != wantG {
			t.Fatalf("n=%d: g = %d, want ceil(%d/2)=%d", n, g, n, wantG)
		}
		if x != n {
			t.Fatalf("n=%d: popped %d, want newest shared %d", n, x, n)
		}
		t.Logf("input: |S|=%d,Pop on empty P; output: %d; basis: g=ceil(%d/2)=%d moves newest to P", n, x, n, g)
	}
}

// TestReleaseOrderingPushVsPop proves the release check runs AFTER the
// Push append but BEFORE the Pop removal.
func TestReleaseOrderingPushVsPop(t *testing.T) {
	// Push ordering: pending miss + Push triggers release; the appended
	// element stays private and the oldest of the old elements go shared.
	d := mustNew(t, 16, 16, 1, 4)
	for x := int64(1); x <= 5; x++ {
		if err := d.Push(x); err != nil {
			t.Fatal(err)
		}
	}
	steal(t, d, 16) // fl, dm=16
	if err := d.Push(6); err != nil {
		t.Fatal(err)
	}
	// |P| after append = 6; r = min(max(3,16), 16, 6-1) = 5 -> S=[1..5].
	if got := steal(t, d, 16); !eqSlice(got, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("push ordering: S = %v, want [1..5]", got)
	}
	if x, ok := d.Pop(); !ok || x != 6 {
		t.Fatalf("push ordering: Pop = (%d,%v), want 6 (appended item stayed private)", x, ok)
	}
	t.Logf("input: Push1..5,miss,Push(6); basis: check after append -> r=min(16,16,5)=5, 6 stays private")

	// Pop ordering: dm small so r = floor(5/2) = 2; check before removal
	// moves [1,2] to S, then pops the private back (5, not 3).
	e := mustNew(t, 16, 16, 1, 4)
	for x := int64(1); x <= 5; x++ {
		if err := e.Push(x); err != nil {
			t.Fatal(err)
		}
	}
	steal(t, e, 2) // dm=2 <= floor(5/2), so r stays 2
	if x, ok := e.Pop(); !ok || x != 5 {
		t.Fatalf("pop ordering: Pop = (%d,%v), want 5", x, ok)
	}
	if got := steal(t, e, 16); !eqSlice(got, []int64{1, 2}) {
		t.Fatalf("pop ordering: S = %v, want [1 2]", got)
	}
	t.Logf("input: Push1..5,Steal(2),Pop; basis: check before removal -> r=min(max(2,2),16,4)=2, pops 5 not 3")
}

// TestFifoVsLifo checks steal FIFO order vs owner LIFO order.
func TestFifoVsLifo(t *testing.T) {
	d := mustNew(t, 16, 8, 0, 4)
	for x := int64(1); x <= 6; x++ {
		if err := d.Push(x); err != nil {
			t.Fatal(err)
		}
	}
	steal(t, d, 16)
	if err := d.Push(7); err != nil {
		t.Fatal(err)
	}
	// r = min(max(3,16), 8, 7) = 7: S=[1..7].
	if got := steal(t, d, 3); !eqSlice(got, []int64{1, 2, 3}) {
		t.Fatalf("steal FIFO = %v, want [1 2 3]", got)
	}
	if got := steal(t, d, 3); !eqSlice(got, []int64{4, 5, 6}) {
		t.Fatalf("steal FIFO = %v, want [4 5 6]", got)
	}
	if x, ok := d.Pop(); !ok || x != 7 {
		t.Fatalf("owner LIFO = (%d,%v), want 7", x, ok)
	}
	t.Logf("input: release-all then Steal x2 and Pop; basis: thief takes oldest FIFO, owner takes newest LIFO")
}
