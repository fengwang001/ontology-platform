// Package window is a fixed-capacity ring buffer of recent bytes.
package window

// Window stores at most Cap() most recently added bytes. Positions are
// addressed by absolute index: position p is available once p < Len(), and
// still retained while Len()-p <= Cap().
type Window struct {
	buf  []byte
	len  int // total bytes ever added
}

// New creates a window of the given fixed capacity.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return cap(w.buf) }

// Len returns the total number of bytes ever added.
func (w *Window) Len() int { return w.len }

// Add appends one byte, evicting the oldest byte when full.
func (w *Window) Add(b byte) {
	w.buf[w.len%cap(w.buf)] = b
	w.len++
}

// At returns the byte stored at absolute position p.
func (w *Window) At(p int) byte {
	return w.buf[p%w.Cap()]
}

// Available reports whether distance d (1-based, looking backwards from the
// most recently added byte) is inside both the history length and capacity.
func (w *Window) Available(d int) bool {
	return d >= 1 && d <= w.Len() && d <= w.Cap()
}
