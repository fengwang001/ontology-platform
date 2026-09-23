package window

import "errors"

var ErrInvalidCapacity = errors.New("window: capacity must be positive")

type Window struct {
	capacity int
}

func New(capacity int) (*Window, error) {
	return &Window{}, nil
}

func (w *Window) Capacity() int { return 0 }
func (w *Window) Len() int      { return 0 }
func (w *Window) Add(p []byte)   {}

func (w *Window) At(distance int) byte { return 0 }

func (w *Window) Tail(n int) []byte { return nil }
