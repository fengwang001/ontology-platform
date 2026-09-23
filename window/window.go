// Package window is a fixed-capacity ring buffer of the most recent bytes.
package window

// Window is a ring buffer holding the newest Cap bytes ever written.
type Window struct {
	buf   []byte
	size  int // valid bytes, capped at cap
	next  int // slot where the next byte is written
	total int64
}

// New returns a window of the given positive capacity.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}
}

// Cap returns the ring capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently held (<= Cap).
func (w *Window) Len() int { return w.size }

// Total returns the number of bytes ever written.
func (w *Window) Total() int64 { return w.total }

// Put appends one byte, evicting the oldest byte when full.
func (w *Window) Put(b byte) {
	w.buf[w.next] = b
	w.next++
	if w.next == len(w.buf) {
		w.next = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
	w.total++
}

// PutSeq appends every byte of b.
func (w *Window) PutSeq(b []byte) {
	for _, c := range b {
		w.Put(c)
	}
}

// At returns the byte distance positions back from the newest byte.
// Distance 1 is the most recently written byte; requires 1 <= d <= Len.
func (w *Window) At(distance int) byte {
	if distance <= 0 || distance > w.size {
		panic("window: distance out of range")
	}
	idx := w.next - distance
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx]
}

// AtAbs returns the byte stored for absolute stream position p (0-based).
// Requires the position to still be inside the ring.
func (w *Window) AtAbs(p int64) byte {
	oldest := w.total - int64(w.size)
	if p < oldest || p >= w.total {
		panic("window: absolute position out of range")
	}
	return w.At(int(w.total - p))
}
