// Package window is a fixed-capacity ring buffer of the most recent bytes.
package window

import "errors"

// ErrBadConfig is returned when the capacity is not positive.
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window keeps at most Cap most recently added bytes in a ring.
// Distances are 1-based: distance 1 is the most recently added byte.
type Window struct {
	buf  []byte
	pos  int // index where the next byte is written
	size int // number of valid bytes (<= cap)
}

// New creates a window of the given fixed capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

func (w *Window) Cap() int  { return len(w.buf) }
func (w *Window) Len() int  { return w.size }

// Add appends one byte, evicting the oldest byte when full.
func (w *Window) Add(b byte) {
	w.buf[w.pos] = b
	w.pos++
	if w.pos == len(w.buf) {
		w.pos = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

// AddBytes appends a slice.
func (w *Window) AddBytes(p []byte) {
	if len(p) >= len(w.buf) {
		copy(w.buf, p[len(p)-len(w.buf):])
		w.pos, w.size = 0, len(w.buf)
	return
	}
	for _, b := range p {
		w.Add(b)
	}
}

func (w *Window) index(distance int) int {
	i := w.pos - distance
	if i < 0 {
		i += len(w.buf)
	}
	return i
}

// At returns the byte at 1-based distance. distance must be 1..Len.
func (w *Window) At(distance int) byte {
	return w.buf[w.index(distance)]
}

// Compare counts how many bytes of p match history starting at 1-based
// distance, stopping at the end of p or available history.
func (w *Window) Compare(distance int, p []byte) int {
	n := 0
	for n < len(p) && distance+n <= w.size {
		if w.At(distance+n) != p[n] {
			break
		}
		n++
	}
	return n
}

// CopyMatch appends a (possibly overlapping: distance < length) back-reference
// to dst, copying one byte at a time so periodic expansion stays correct.
func (w *Window) CopyMatch(dst []byte, distance, length int) []byte {
	for i := 0; i < length; i++ {
		b := w.At(distance)
		dst = append(dst, b)
		w.Add(b)
	}
	return dst
}
