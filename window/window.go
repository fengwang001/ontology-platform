package window

import "errors"

var ErrInvalidCapacity = errors.New("window: capacity must be positive")

type Window struct {
	capacity int
}

func New(capacity int) (*Window, error) {
	return nil, nil
}

func (w *Window) Capacity() int { return 0 }

func (w *Window) Len() int { return 0 }

func (w *Window) Put(b byte) {}

func (w *Window) PutAll(p []byte) {}

func (w *Window) Byte(distance int) (byte, bool) { return 0, false }
