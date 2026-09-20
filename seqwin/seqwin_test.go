package seqwin

import "testing"

// Semantics 1: sequence numbers start at 1; 0 is always Invalid and
// must not change any state.
func TestZeroIsInvalid(t *testing.T) {
	w := New(8)
	if v := w.Accept(0); v != Invalid {
		t.Fatalf("Accept(0) = %v, want Invalid", v)
	}
	if w.Highest() != 0 {
		t.Fatalf("Highest() = %d, want 0", w.Highest())
	}
	if w.Seen(0) {
		t.Fatal("Seen(0) = true, want false")
	}
	w.Accept(5)
	w.Accept(0)
	if w.Highest() != 5 {
		t.Fatalf("Highest() = %d after Accept(0), want 5", w.Highest())
	}
}

// Semantics 2: larger sequence numbers push the window right; numbers
// pushed past the left edge become TooOld.
func TestWindowSlidesRight(t *testing.T) {
	w := New(2)
	for _, seq := range []uint64{1, 2, 3} {
		if v := w.Accept(seq); v != Fresh {
			t.Fatalf("Accept(%d) = %v, want Fresh", seq, v)
		}
	}
	if v := w.Accept(1); v != TooOld {
		t.Fatalf("Accept(1) = %v, want TooOld", v)
	}
	if w.Highest() != 3 {
		t.Fatalf("Highest() = %d, want 3", w.Highest())
	}
}

// Semantics 3: out-of-order but in-window and unseen is Fresh; the
// same number again is Duplicate.
func TestOutOfOrderWithinWindow(t *testing.T) {
	w := New(8)
	w.Accept(10)
	if v := w.Accept(7); v != Fresh {
		t.Fatalf("Accept(7) = %v, want Fresh", v)
	}
	if v := w.Accept(7); v != Duplicate {
		t.Fatalf("Accept(7) again = %v, want Duplicate", v)
	}
	if !w.Seen(7) || !w.Seen(10) {
		t.Fatal("Seen should report 7 and 10 as recorded")
	}
	if w.Seen(8) {
		t.Fatal("Seen(8) = true, want false")
	}
}

// Semantics 4: exact boundary. With size=8 and Highest=100 the window
// is [93, 100]: 93 is inside, 92 is TooOld.
func TestBoundaryExact(t *testing.T) {
	w := New(8)
	w.Accept(100)
	if v := w.Accept(93); v != Fresh {
		t.Fatalf("Accept(93) = %v, want Fresh (window is [93,100])", v)
	}
	if v := w.Accept(92); v != TooOld {
		t.Fatalf("Accept(92) = %v, want TooOld", v)
	}
	if w.Seen(92) {
		t.Fatal("Seen(92) = true, want false")
	}
}

// Semantics 5: a huge jump must not leak stale state. After Highest
// goes 10 -> 10000 with a small window, everything in the new window
// except 10000 itself is unseen.
func TestBigJumpClearsWindow(t *testing.T) {
	w := New(8)
	w.Accept(10)
	if v := w.Accept(10000); v != Fresh {
		t.Fatalf("Accept(10000) = %v, want Fresh", v)
	}
	if v := w.Accept(9999); v != Fresh {
		t.Fatalf("Accept(9999) = %v, want Fresh", v)
	}
	if v := w.Accept(10); v != TooOld {
		t.Fatalf("Accept(10) = %v, want TooOld", v)
	}
	if !w.Seen(10000) {
		t.Fatal("Seen(10000) = false, want true (clearing must not drop the new edge)")
	}
	if w.Seen(9998) {
		t.Fatal("Seen(9998) = true, want false")
	}
}

// Semantics 6: re-accepting the current Highest is a Duplicate and
// does not move the edge.
func TestDuplicateHighest(t *testing.T) {
	w := New(8)
	w.Accept(5)
	if v := w.Accept(5); v != Duplicate {
		t.Fatalf("Accept(5) again = %v, want Duplicate", v)
	}
	if w.Highest() != 5 {
		t.Fatalf("Highest() = %d, want 5", w.Highest())
	}
}

// Semantics 7: memory stays proportional to size only. After 100k
// consecutive sequence numbers the bitmap must not have grown.
func TestBitmapSizeIsConstant(t *testing.T) {
	w := New(64)
	before := len(w.seen.bits)
	for seq := uint64(1); seq <= 100000; seq++ {
		if v := w.Accept(seq); v != Fresh {
			t.Fatalf("Accept(%d) = %v, want Fresh", seq, v)
		}
	}
	if got := len(w.seen.bits); got != before {
		t.Fatalf("bitmap words = %d, want %d (size-dependent only)", got, before)
	}
	if want := (64 + 63) / 64; len(w.seen.bits) != want {
		t.Fatalf("bitmap words = %d, want %d for size 64", len(w.seen.bits), want)
	}
	w2 := New(100)
	if want := (100 + 63) / 64; len(w2.seen.bits) != want {
		t.Fatalf("bitmap words = %d, want %d for size 100", len(w2.seen.bits), want)
	}
}

// New with size <= 0 behaves as size 1.
func TestNonPositiveSizeIsOne(t *testing.T) {
	for _, size := range []int{0, -3} {
		w := New(size)
		w.Accept(1)
		w.Accept(2)
		if v := w.Accept(1); v != TooOld {
			t.Fatalf("New(%d): Accept(1) = %v, want TooOld", size, v)
		}
	}
}
