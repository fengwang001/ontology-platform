package window

import "errors"

var ErrZeroSize = errors.New("window size must be positive")

type Window struct {
	buf    []byte
	size   uint64
	total  uint64
	filled uint64
}

func New(size uint64) (*Window, error) {
	if size == 0 {
		return nil, ErrZeroSize
	}
	return &Window{buf: make([]byte, size), size: size}, nil
}

func (w *Window) Size() uint64 { return w.size }

func (w *Window) Written() uint64 { return w.total }

func (w *Window) Reset(dict []byte) {
	w.total, w.filled = 0, 0
	for _, b := range dict {
		w.Put(b)
	}
}

func (w *Window) Put(b byte) {
	if w.filled < w.size {
		w.buf[w.filled] = b
		w.filled++
	} else {
		w.buf[w.total%w.size] = b
	}
	w.total++
}

func (w *Window) At(distance uint64) (byte, bool) {
	if distance == 0 || distance > w.total || distance > w.filled {
		return 0, false
	}
	idx := (w.total - distance) % w.size
	return w.buf[idx], true
}
