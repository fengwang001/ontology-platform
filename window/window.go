package window

import "errors"

var ErrInvalidCapacity = errors.New("window capacity must be positive")

type Window struct {
	data     []byte
	start    int
	size     int
	capacity int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Window{data: make([]byte, capacity), capacity: capacity}, nil
}

func (w *Window) Capacity() int { return w.capacity }
func (w *Window) Len() int      { return w.size }

func (w *Window) Add(data []byte) {
	for _, b := range data {
		if w.size == w.capacity {
			w.data[w.start] = b
			w.start++
			if w.start == w.capacity {
				w.start = 0
			}
			continue
		}
		index := (w.start + w.size) % w.capacity
		w.data[index] = b
		w.size++
	}
}

func (w *Window) AddByte(b byte) {
	w.Add([]byte{b})
}

func (w *Window) At(distance int) (byte, bool) {
	if distance <= 0 || distance > w.size {
		return 0, false
	}
	index := (w.start + w.size - distance) % w.capacity
	return w.data[index], true
}

func (w *Window) Tail(n int) []byte {
	if n > w.size {
		n = w.size
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i], _ = w.At(n - i)
	}
	return out
}
