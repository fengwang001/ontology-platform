package window

import "errors"

var ErrConfig = errors.New("window capacity must be positive")

type Window struct {
	data []byte
	head int
	size int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrConfig
	}
	return &Window{data: make([]byte, capacity)}, nil
}

func (w *Window) Cap() int { return cap(w.data) }

func (w *Window) Len() int { return w.size }

func (w *Window) Put(b byte) {
	w.data[w.head] = b
	w.head++
	if w.head == cap(w.data) {
		w.head = 0
	}
	if w.size < cap(w.data) {
		w.size++
	}
}

func (w *Window) Write(p []byte) {
	for _, b := range p {
		w.Put(b)
	}
}

// Get returns the byte distance positions before the newest byte.
func (w *Window) Get(distance int) (byte, bool) {
	if distance <= 0 || distance > w.size {
		return 0, false
	}
	idx := w.head - distance
	if idx < 0 {
		idx += cap(w.data)
	}
	return w.data[idx], true
}
