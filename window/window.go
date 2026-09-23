package window

import "errors"

var ErrInvalidConfig = errors.New("window: capacity must be positive")

type Window struct {
	data []byte
	next int
	size int
	cap  int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Window{data: make([]byte, capacity), cap: capacity}, nil
}

func (w *Window) Capacity() int { return w.cap }

func (w *Window) Len() int { return w.size }

func (w *Window) Add(b byte) {
	w.data[w.next] = b
	w.next++
	if w.next == w.cap {
		w.next = 0
	}
	if w.size < w.cap {
		w.size++
	}
}

func (w *Window) Preset(p []byte) {
	if len(p) > w.cap {
		p = p[len(p)-w.cap:]
	}
	for _, b := range p {
		w.Add(b)
	}
}

func (w *Window) At(distance int) byte {
	if distance <= 0 || distance > w.size {
		panic("window: distance out of range")
	}
	idx := w.next - distance
	if idx < 0 {
		idx += w.cap
	}
	return w.data[idx]
}

func (w *Window) Reset() {
	w.next, w.size = 0, 0
}
