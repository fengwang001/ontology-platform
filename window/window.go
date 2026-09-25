// Package window is a fixed-capacity ring buffer of recent history
// bytes, addressed by absolute position. It depends on nothing.
package window

// Window keeps the last Cap() appended bytes.
type Window struct {
	buf []byte
	n   int // total bytes ever appended (absolute position of next byte)
}

// New returns a Window with the given capacity. cap must be > 0;
// callers are expected to validate configuration beforehand.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}
}

// Cap returns the window capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the total number of bytes appended so far, i.e. the
// absolute position that the next appended byte will occupy.
func (w *Window) Len() int { return w.n }

// Append stores b at the next absolute position, evicting the oldest
// byte when full.
func (w *Window) Append(b byte) {
	w.buf[w.n%len(w.buf)] = b
	w.n++
}

// At returns the byte at absolute position pos. It panics if pos is
// out of the retained range [Len()-Cap(), Len()).
func (w *Window) At(pos int) byte {
	if pos < 0 || pos >= w.n || pos < w.n-len(w.buf) {
		panic("window: position out of range")
	}
	return w.buf[pos%len(w.buf)]
}

// Preload appends a dictionary of historical bytes (e.g. the tail of
// the previous block for parallel compression). Only the last Cap()
// bytes are retained.
func (w *Window) Preload(dict []byte) {
	if len(dict) > len(w.buf) {
		dict = dict[len(dict)-len(w.buf):]
	}
	for _, b := range dict {
		w.Append(b)
	}
}
