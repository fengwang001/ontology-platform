package refheap

import "testing"

// Exact-fit allocation succeeds; one byte more fails; refs count too.
func TestHeapExactFit(t *testing.T) {
	h, _ := NewHeap(100, 10, 5)
	mustOK(h.Alloc(60, 0, false))
	mustOK(h.Alloc(40, 0, false))
	if h.Used() != 100 {
		t.Fatalf("used = %d", h.Used())
	}
	_, err := h.Alloc(1, 0, false)
	assertErr(t, err, ErrHeapFull)

	h2, _ := NewHeap(10, 10, 5)
	mustOK(h2.Alloc(5, 0, false))
	_, err = h2.NewRef(Weak, 0, 0, 6, 0)
	assertErr(t, err, ErrHeapFull)
	mustOK(h2.NewRef(Weak, 0, 0, 5, 0))
}

func TestRejectionOrdering(t *testing.T) {
	h, _ := NewHeap(1000, 10, 5)
	o := mustOK(h.Alloc(10, 1, false))
	r := mustOK(h.NewRef(Weak, o, 0, 5, 0))

	// NewHeap: invalid arg.
	if _, err := NewHeap(0, 10, 5); codeOf(err) != ErrInvalidArg {
		t.Fatalf("NewHeap bad C: %v", err)
	}
	if _, err := NewHeap(10, 0, 5); codeOf(err) != ErrInvalidArg {
		t.Fatalf("NewHeap bad U: %v", err)
	}
	if _, err := NewHeap(10, 1, -1); codeOf(err) != ErrInvalidArg {
		t.Fatalf("NewHeap bad M: %v", err)
	}

	// Alloc invalid beats full.
	small, _ := NewHeap(5, 10, 5)
	_, err := small.Alloc(0, 0, false)
	assertErr(t, err, ErrInvalidArg)
	_, err = small.Alloc(6, -1, false)
	assertErr(t, err, ErrInvalidArg)
	_, err = small.Alloc(6, 9, false)
	assertErr(t, err, ErrInvalidArg)

	// NewRef: invalid arg, then missing target/queue, then clock, then full.
	_, err = h.NewRef(RefKind(9), o, 0, 5, 0)
	assertErr(t, err, ErrInvalidArg)
	_, err = h.NewRef(Weak, o, 0, 0, 0)
	assertErr(t, err, ErrInvalidArg)
	_, err = h.NewRef(Weak, o, 0, 5, -1)
	assertErr(t, err, ErrInvalidArg)
	_, err = h.NewRef(Weak, 999, 0, 5, 0)
	assertErr(t, err, ErrNotFound)
	_, err = h.NewRef(Weak, o, 77, 5, 0)
	assertErr(t, err, ErrNotFound)
	h.mu.Lock()
	h.maxNow = 100
	h.mu.Unlock()
	_, err = h.NewRef(Weak, 999, 0, 5, 50)
	assertErr(t, err, ErrNotFound) // existence beats clock
	_, err = h.NewRef(Weak, o, 0, 5, 50)
	assertErr(t, err, ErrClockBackward)
	full, _ := NewHeap(10, 10, 5)
	_, err = full.NewRef(Weak, 0, 0, 11, 0)
	assertErr(t, err, ErrHeapFull)

	// SetField: index range, existence, wrong kind, then per-field index,
	// then target existence.
	err = h.SetField(999, 8, 0)
	assertErr(t, err, ErrInvalidArg) // i>=8 is invalid before existence
	err = h.SetField(999, 0, 0)
	assertErr(t, err, ErrNotFound)
	err = h.SetField(r, 0, 0)
	assertErr(t, err, ErrWrongKind)
	err = h.SetField(o, 5, 0)
	assertErr(t, err, ErrInvalidArg) // object only has 1 field
	err = h.SetField(o, 0, 999)
	assertErr(t, err, ErrNotFound)

	// SetRoot/ClearRoot.
	assertErr(t, h.SetRoot(999), ErrNotFound)
	assertErr(t, h.ClearRoot(999), ErrNotFound)

	// Get: invalid now, missing, wrong kind, clock.
	_, err = h.Get(r, -1)
	assertErr(t, err, ErrInvalidArg)
	_, err = h.Get(999, 200)
	assertErr(t, err, ErrNotFound)
	_, err = h.Get(o, 200)
	assertErr(t, err, ErrWrongKind)
	_, err = h.Get(r, 50)
	assertErr(t, err, ErrClockBackward)

	// Poll missing queue.
	_, err = h.Poll(424242)
	assertErr(t, err, ErrNotFound)

	// Finalize range.
	_, err = h.Finalize(-1)
	assertErr(t, err, ErrInvalidArg)
	_, err = h.Finalize(maxFinalN + 1)
	assertErr(t, err, ErrInvalidArg)

	// Collect: invalid now, then clock.
	_, err = h.Collect(maxNow + 1)
	assertErr(t, err, ErrInvalidArg)
	_, err = h.Collect(50)
	assertErr(t, err, ErrClockBackward)
}

// A rejected operation changes nothing: ids, queues, clock and used bytes.
func TestRejectionChangesNothing(t *testing.T) {
	h, _ := NewHeap(1000, 10, 5)
	q := h.CreateQueue()
	o := mustOK(h.Alloc(500, 1, false))
	r := mustOK(h.NewRef(Weak, o, q, 5, 10))
	mustOKInt(h.SetRoot(r))

	h.mu.Lock()
	beforeID := h.nextID
	beforeQ := h.nextQ
	beforeNow := h.maxNow
	beforeUsed := h.used
	h.mu.Unlock()

	_, _ = h.Alloc(600, 0, false)       // heap full
	_, _ = h.Alloc(0, 0, false)         // invalid
	_, _ = h.NewRef(Weak, 999, 0, 1, 5) // missing
	_, _ = h.NewRef(Weak, o, 0, 1, 5)   // clock back
	_ = h.SetField(o, 0, 999)           // missing target
	_ = h.SetField(r, 0, 0)             // wrong kind
	_, _ = h.Get(999, 20)               // missing
	_, _ = h.Get(o, 20)                 // wrong kind
	_, _ = h.Get(r, 5)                  // clock back
	_, _ = h.Poll(999)                  // missing
	_, _ = h.Finalize(-1)               // invalid
	_, _ = h.Collect(5)                 // clock back

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.nextID != beforeID || h.nextQ != beforeQ ||
		h.maxNow != beforeNow || h.used != beforeUsed {
		t.Fatalf("state changed by rejected op: ids %d->%d q %d->%d now %d->%d used %d->%d",
			beforeID, h.nextID, beforeQ, h.nextQ, beforeNow, h.maxNow, beforeUsed, h.used)
	}
	if h.objs[r].target != o {
		t.Fatalf("target changed: %d", h.objs[r].target)
	}
}
