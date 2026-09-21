package seqwin

import (
	"math"
	"testing"
)

// Regression for defect 1: Accept had no seq==0 check, so 0 fell
// through to the TooOld branch and was misjudged instead of Invalid.
func TestAcceptZeroNeverTouchesState(t *testing.T) {
	w := New(4)
	for _, seq := range []uint64{1, 2, 3, 4, 5} {
		w.Accept(seq)
	}
	before := w.Highest()
	if v := w.Accept(0); v != Invalid {
		t.Fatalf("Accept(0) = %v, want Invalid", v)
	}
	if w.Highest() != before {
		t.Fatalf("Highest() = %d after Accept(0), want %d", w.Highest(), before)
	}
	if w.Seen(0) {
		t.Fatal("Seen(0) = true, want false")
	}
	// The window contents must be undisturbed: 2 is still in-window
	// and recorded, so a replay of it is a Duplicate.
	if v := w.Accept(2); v != Duplicate {
		t.Fatalf("Accept(2) = %v, want Duplicate (window must be untouched)", v)
	}
}

// Regression for defect 2: Accept used `seq <= lowest` for the TooOld
// check, misjudging the sequence number exactly on the left edge
// (Highest-size+1) as TooOld; the comparison must be strict.
func TestLeftEdgeSequenceIsInWindow(t *testing.T) {
	w := New(4)
	w.Accept(10) // window is now [7, 10]
	if v := w.Accept(7); v != Fresh {
		t.Fatalf("Accept(7) = %v, want Fresh (7 == Highest-size+1 is in-window)", v)
	}
	if v := w.Accept(7); v != Duplicate {
		t.Fatalf("Accept(7) again = %v, want Duplicate", v)
	}
	if v := w.Accept(6); v != TooOld {
		t.Fatalf("Accept(6) = %v, want TooOld (6 == Highest-size is evicted)", v)
	}
	if !w.Seen(7) {
		t.Fatal("Seen(7) = false, want true")
	}
	if w.Seen(6) {
		t.Fatal("Seen(6) = true, want false")
	}
}

// Regression for defect 3 (the one no existing test or demo check
// caught): Seen had no upper-bound check, so for seq > Highest the
// unsigned subtraction Highest-seq wrapped around, indexing the
// bitmap out of range (panic) or reporting a never-received sequence
// number as seen.
func TestSeenAboveHighestIsFalse(t *testing.T) {
	w := New(7)
	w.Accept(1)
	for _, seq := range []uint64{2, 100, math.MaxUint64} {
		if w.Seen(seq) {
			t.Fatalf("Seen(%d) = true, want false (never received, above Highest)", seq)
		}
	}
	// A full window must not change the answer for future sequence
	// numbers either.
	w2 := New(8)
	for seq := uint64(1); seq <= 100; seq++ {
		w2.Accept(seq)
	}
	for _, seq := range []uint64{101, 1000, math.MaxUint64} {
		if w2.Seen(seq) {
			t.Fatalf("Seen(%d) = true, want false (above Highest=100)", seq)
		}
	}
	if !w2.Seen(100) {
		t.Fatal("Seen(100) = false, want true")
	}
}
