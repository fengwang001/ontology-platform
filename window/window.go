// Package window is the fixed-capacity sliding history used by the matcher and
// the decompressor. Distance 1 is the most recently written byte.
package window

// Window is a ring buffer holding at most Cap most recent bytes.
type Window struct {
	buf []byte
	pos int // index of the next byte to overwrite
	n   int // live bytes (<= cap)
}

// New creates a window of the given positive capacity.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns how many bytes are currently held.
func (w *Window) Len() int { return w.n }

// Reset empties the window.
func (w *Window) Reset() { w.pos, w.n = 0, 0 }

// Add appends one byte, evicting the oldest byte when full.
func (w *Window) Add(c byte) {
	w.buf[w.pos] = c
	w.pos++
	if w.pos == len(w.buf) {
		w.pos = 0
	}
	if w.n < len(w.buf) {
		w.n++
	}
}

// Preload replaces content with up to Cap trailing bytes of dict, oldest first.
func (w *Window) Preload(dict []byte) {
	w.Reset()
	if len(dict) > len(w.buf) {
		dict = dict[len(dict)-len(w.buf):]
	}
	for _, c := range dict {
		w.Add(c)
	}
}

// At returns the byte at distance d (1..Len); ok is false out of range.
func (w *Window) At(d int) (byte, bool) {
	if d <= 0 || d > w.n {
		return 0, false
	}
	i := w.pos - d
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i], true
}

// EqualAt reports whether the byte at distance d equals c.
func (w *Window) EqualAt(d int, c byte) bool {
	got, ok := w.At(d)
	return ok && got == c
}
