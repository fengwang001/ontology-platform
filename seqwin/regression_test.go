package seqwin

import (
	"math"
	"testing"
)

// Regression: Accept had no zero check, so 0 fell through to the
// window logic and came back TooOld instead of Invalid.
func TestAcceptZeroIsInvalidNotTooOld(t *testing.T) {
	w := New(4)
	if v := w.Accept(0); v != Invalid {
		t.Fatalf("Accept(0) = %v, want Invalid", v)
	}
	if w.Highest() != 0 {
		t.Fatalf("Highest() = %d after Accept(0), want 0", w.Highest())
	}
}

// Regression: Accept used seq <= lowest() for the TooOld check,
// wrongly rejecting the oldest sequence number still inside the
// window (and every Accept(1) once lowest() reached 1).
func TestLeftEdgeStaysInWindow(t *testing.T) {
	w := New(4)
	w.Accept(10) // window is now [7, 10]
	if v := w.Accept(7); v != Fresh {
		t.Fatalf("Accept(7) = %v, want Fresh (window is [7,10])", v)
	}
	if v := w.Accept(6); v != TooOld {
		t.Fatalf("Accept(6) = %v, want TooOld", v)
	}
}

// Regression: Seen lacked an upper-bound check, so for seq > Highest
// the unsigned subtraction highest-seq wrapped around and indexed the
// bitmap out of range (panic) or lied about a never-received number.
func TestSeenAboveHighestIsFalse(t *testing.T) {
	w := New(7)
	w.Accept(1)
	for _, seq := range []uint64{2, 1000, math.MaxUint64} {
		if w.Seen(seq) {
			t.Fatalf("Seen(%d) = true, want false (never received)", seq)
		}
	}
}
