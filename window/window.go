package window

import "errors"

var ErrConfig = errors.New("window: capacity must be positive")

// Window is a fixed-capacity ring of the most recently written bytes.
// Positions are absolute: the first byte ever written has position 0.
type Window struct {
	buf    []byte
	cap    int
	written int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrConfig
	}
	return &Window{buf: make([]byte, 0, capacity), cap: capacity}, nil
}

// Add appends bytes; oldest bytes are discarded once capacity is exceeded.
func (w *Window) Add(p []byte) {
	for _, c := range p {
		if len(w.buf) < w.cap {
			w.buf = append(w.buf, c)
		} else {
			w.buf[w.written%w.cap] = c
		}
		w.written++
	}
}

func (w *Window) Written() int { return w.written }
func (w *Window) Cap() int     { return w.cap }

// Available reports how many bytes a distance may legally reference.
func (w *Window) Available() int {
	if w.written < w.cap {
		return w.written
	}
	return w.cap
}

// At returns the byte at absolute position pos.
func (w *Window) At(pos int) byte {
	if len(w.buf) < w.cap {
		return w.buf[pos]
	}
	return w.buf[pos%w.cap]
}

// ByteAtDistance returns the byte that is dist bytes before the current
// write cursor: dist 1 means the most recently written byte.
func (w *Window) ByteAtDistance(dist int) byte {
	return w.At(w.written - dist)
}

// Copy appends one byte at distance dist to dst and returns it, honoring
// overlap semantics (the copied byte becomes the newest history).
func (w *Window) Copy(dst []byte, dist int) []byte {
	c := w.ByteAtDistance(dist)
	w.Add([]byte{c})
	return append(dst, c)
}
