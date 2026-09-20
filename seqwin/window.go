package seqwin

import "sync"

// Window is a sliding-window replay detector over sequence numbers
// starting at 1. The window always covers the closed interval
// [Highest-size+1, Highest]; anything left of it is TooOld. It is
// safe for concurrent use.
type Window struct {
	mu      sync.Mutex
	highest uint64
	size    int
	seen    bitmap
}

// New returns a Window of the given width (number of sequence numbers
// it can track). A size <= 0 is treated as 1.
func New(size int) *Window {
	if size <= 0 {
		size = 1
	}
	return &Window{size: size, seen: newBitmap(size)}
}

// Accept judges one sequence number and, when the verdict is Fresh,
// records it in the window.
func (w *Window) Accept(seq uint64) Verdict {
	if seq == 0 {
		return Invalid
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if seq > w.highest {
		// Advance the right edge; bits pushed past the left edge are
		// dropped, so those sequence numbers become TooOld.
		d := seq - w.highest
		if d >= uint64(w.size) {
			w.seen.shiftLeft(w.size)
		} else {
			w.seen.shiftLeft(int(d))
		}
		w.highest = seq
		w.seen.set(0)
		return Fresh
	}

	if seq < w.lowest() {
		return TooOld
	}
	i := int(w.highest - seq)
	if w.seen.get(i) {
		return Duplicate
	}
	w.seen.set(i)
	return Fresh
}

// Highest returns the largest sequence number seen so far, or 0 if no
// packet has been accepted yet.
func (w *Window) Highest() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.highest
}

// Seen reports whether the sequence number is currently recorded in
// the window, without modifying any state.
func (w *Window) Seen(seq uint64) bool {
	if seq == 0 {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if seq > w.highest || seq < w.lowest() {
		return false
	}
	return w.seen.get(int(w.highest - seq))
}

// lowest returns the smallest sequence number still inside the
// window. The caller must hold w.mu.
func (w *Window) lowest() uint64 {
	if w.highest <= uint64(w.size) {
		return 1
	}
	return w.highest - uint64(w.size) + 1
}
