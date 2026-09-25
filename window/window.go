package window

import "errors"

var ErrDistance = errors.New("window: invalid distance")

type Window struct {
	buf   []byte
	size  int
	total int
	next  int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, errors.New("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity), size: capacity}, nil
}

func (w *Window) Add(p byte) {
	w.buf[w.next] = p
	w.next++
	if w.next == w.size {
		w.next = 0
	}
	w.total++
}

func (w *Window) AddBytes(p []byte) {
	for _, b := range p {
		w.Add(b)
	}
}

func (w *Window) Len() int {
	if w.total < w.size {
		return w.total
	}
	return w.size
}

func (w *Window) Total() int { return w.total }

func (w *Window) Cap() int { return w.size }

func (w *Window) Byte(distance int) (byte, error) {
	if distance <= 0 || distance > w.Len() {
		return 0, ErrDistance
	}
	index := w.next - distance
	if index < 0 {
		index += w.size
	}
	return w.buf[index], nil
}

func (w *Window) Copy(dst []byte, distance, length int) error {
	if distance <= 0 || distance > w.Len() {
		return ErrDistance
	}
	for i := 0; i < length; i++ {
		b, err := w.Byte(distance)
		if err != nil {
			return err
		}
		dst[i] = b
		w.Add(b)
	}
	return nil
}
