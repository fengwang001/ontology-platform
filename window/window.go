package window

import "errors"

var ErrInvalidCapacity = errors.New("window: capacity must be positive")

type Window struct {
	data []byte
	next int
	size int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Window{data: make([]byte, capacity)}, nil
}

func (w *Window) Capacity() int { return len(w.data) }

func (w *Window) Len() int { return w.size }

func (w *Window) Add(b byte) {
	w.data[w.next] = b
	w.next++
	if w.next == len(w.data) {
		w.next = 0
	}
	if w.size < len(w.data) {
		w.size++
	}
}

func (w *Window) AddBytes(data []byte) {
	for _, b := range data {
		w.Add(b)
	}
}

func (w *Window) Byte(distance int) byte {
	if distance <= 0 || distance > w.size {
		panic("window: invalid distance")
	}
	index := w.next - distance
	if index < 0 {
		index += len(w.data)
	}
	return w.data[index]
}
