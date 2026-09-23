package window

import "errors"

const MaxCapacity = 1 << 20

var (
	ErrCapacity = errors.New("lz77: window capacity must be positive")
	ErrDistance = errors.New("lz77: distance exceeds available history")
)

type Window struct {
	buf   []byte
	size  int
	start int
	total int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 || capacity > MaxCapacity {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

func (w *Window) Capacity() int { return len(w.buf) }
func (w *Window) Len() int      { return w.size }
func (w *Window) Total() int    { return w.total }

func (w *Window) Reset() {
	w.size, w.start, w.total = 0, 0, 0
}

func (w *Window) Add(p []byte) {
	capn := len(w.buf)
	for _, b := range p {
		w.buf[w.start] = b
		w.start++
		if w.start == capn {
			w.start = 0
		}
		if w.size < capn {
			w.size++
		}
		w.total++
	}
}

func (w *Window) Preset(p []byte) {
	w.Reset()
	if len(p) > len(w.buf) {
		p = p[len(p)-len(w.buf):]
	}
	w.Add(p)
}

func (w *Window) ByteAt(distance int) (byte, error) {
	if distance <= 0 || distance > w.size {
		return 0, ErrDistance
	}
	idx := w.start - distance
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx], nil
}

func (w *Window) MatchLength(data []byte, pos, distance, limit int) int {
	if distance <= 0 || distance > w.size || pos < 0 || pos > len(data) {
		return 0
	}
	if limit > len(data)-pos {
		limit = len(data) - pos
	}
	idx := w.start - distance
	if idx < 0 {
		idx += len(w.buf)
	}
	n := 0
	for n < limit {
		if data[pos+n] != w.buf[idx] {
			break
		}
		n++
		idx++
	if idx == len(w.buf) {
			idx = 0
		}
	}
	return n
}
