// Package window implements a fixed-capacity ring buffer of recent bytes.
// It depends on no other package in this module.
package window

// Window is a ring buffer keeping at most Cap bytes of history. Bytes are
// addressed by absolute stream positions once total is below Cap.
type Window struct {
	buf   []byte
	total int // total bytes ever written (absolute positions 0..total-1)
}

// New returns a window of the given positive capacity.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}
}

// Cap reports the ring capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len reports how many valid bytes are currently held.
func (w *Window) Len() int {
	if w.total < len(w.buf) {
		return w.total
	}
	return len(w.buf)
}

// Total reports how many bytes have ever been written.
func (w *Window) Total() int { return w.total }

// AppendByte records one byte at the next absolute position.
func (w *Window) AppendByte(c byte) {
	w.buf[w.total%len(w.buf)] = c
	w.total++
}

// Append records a contiguous slice.
func (w *Window) Append(p []byte) {
	for _, c := range p {
		w.AppendByte(c)
	}
}

// At returns the byte at absolute position pos. pos must be within the live
// window: Total-Cap <= pos < Total.
func (w *Window) At(pos int) byte {
	if pos < 0 || pos >= w.total || w.total-pos > len(w.buf) {
		panic("window: position outside live window")
	}
	return w.buf[pos%len(w.buf)]
}

// Copy appends length bytes taken at distance dist (1-based) to dst, using
// forward byte-at-a-time semantics so overlapping copies (dist < length)
// reproduce repeated patterns correctly. It also records the bytes into the
// window itself and returns the extended slice.
func (w *Window) Copy(dst []byte, dist, length int) []byte {
	if dist <= 0 || dist > w.Len() {
		panic("window: invalid copy distance")
	}
	start := w.total
	for i := 0; i < length; i++ {
		c := w.At(start + i - dist)
		dst = append(dst, c)
		w.AppendByte(c)
	}
	return dst
}
