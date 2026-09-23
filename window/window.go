package window

import "errors"

var ErrInvalidCapacity = errors.New("window: capacity must be positive")

// Window is a fixed-capacity byte ring. It is not safe for concurrent use.
type Window struct {
	data []byte
	mask uint64
	base uint64
	size int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	size := 1
	for size < capacity {
		size <<= 1
	}
	return &Window{data: make([]byte, size), mask: uint64(size - 1)}, nil
}

func (w *Window) Capacity() int {
	return cap(w.data)
}

func (w *Window) Len() int {
	return w.size
}

// Base is the absolute position of the oldest retained byte.
func (w *Window) Base() uint64 {
	return w.base
}

// Add appends a byte and forgets the oldest byte when capacity is exceeded.
func (w *Window) Add(b byte) {
	pos := w.base + uint64(w.size)
	if w.size == len(w.data) {
		w.data[w.base&w.mask] = b
		w.base++
		return
	}
	w.data[pos&w.mask] = b
	w.size++
}

// At returns a byte by distance from the newest end; distance one is newest.
func (w *Window) At(distance int) (byte, bool) {
	if distance <= 0 || distance > w.size {
		return 0, false
	}
	pos := w.base + uint64(w.size-distance)
	return w.data[pos&w.mask], true
}

// AtPosition returns a byte by its absolute input position.
func (w *Window) AtPosition(pos uint64) (byte, bool) {
	end := w.base + uint64(w.size)
	if pos < w.base || pos >= end {
		return 0, false
	}
	return w.data[pos&w.mask], true
}

func (w *Window) Reset() {
	w.base = 0
	w.size = 0
}
