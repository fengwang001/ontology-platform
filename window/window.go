package window

import "errors"

var ErrInvalidCapacity = errors.New("lz77: window capacity must be positive")

type Window struct {
	data   []byte
	start  int
	length int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Window{data: make([]byte, capacity)}, nil
}

func (w *Window) Capacity() int { return len(w.data) }

func (w *Window) Len() int { return w.length }

func (w *Window) Reset() { w.start, w.length = 0, 0 }

func (w *Window) WriteByte(value byte) {
	if w.length == len(w.data) {
		w.data[w.start] = value
		w.start = (w.start + 1) % len(w.data)
		return
	}
	idx := (w.start + w.length) % len(w.data)
	w.data[idx] = value
	w.length++
}

func (w *Window) Write(data []byte) {
	if len(data) >= len(w.data) {
		copy(w.data, data[len(data)-len(w.data):])
		w.start, w.length = 0, len(w.data)
		return
	}
	for _, value := range data {
		w.WriteByte(value)
	}
}

func (w *Window) At(distance int) byte {
	idx := (w.start + w.length - distance) % len(w.data)
	return w.data[idx]
}

func (w *Window) Copy(dst []byte, distance, length int) int {
	if distance < 1 || distance > w.length {
		return 0
	}
	count := min(length, len(dst))
	for i := 0; i < count; i++ {
		dst[i] = w.At(distance)
	}
	return count
}
