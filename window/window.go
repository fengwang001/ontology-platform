// Package window implements a fixed-capacity ring buffer of recent bytes.
package window

import "errors"

// ErrBadConfig is returned for an illegal configuration.
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window stores the last Cap bytes written to it.
// Position numbering: the whole stream uses monotonically increasing absolute
// positions; Len is the number of bytes currently available (<= Cap).
type Window struct {
	data    []byte
	cap     int
	len     int
	written int
	head    int // index where the next byte is written
}

// New creates a window with the given positive capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{data: make([]byte, capacity), cap: capacity}, nil
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return w.cap }

// Len returns the number of stored bytes.
func (w *Window) Len() int { return w.len }

// Reset empties the window.
func (w *Window) Reset() { w.len, w.head, w.written = 0, 0, 0 }

// Push appends one byte, overwriting the oldest byte when full.
func (w *Window) Push(b byte) {
	w.data[w.head] = b
	w.head++
	if w.head == w.cap {
		w.head = 0
	}
	if w.len < w.cap {
		w.len++
	}
	w.written++
}

// PushN appends all bytes of p.
func (w *Window) PushN(p []byte) {
	for _, b := range p {
		w.Push(b)
	}
}

// At returns the byte at absolute position pos.
// pos must be within [max(0, written-Cap), written); false is returned otherwise.
func (w *Window) At(pos int) (byte, bool) {
	oldest := w.written - w.len
	if pos < oldest || pos >= w.written {
		return 0, false
	}
	return w.data[pos%w.cap], true
}
