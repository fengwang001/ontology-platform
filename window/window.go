package window

import "errors"

var ErrIllegalConfig = errors.New("window: capacity must be positive")

type Window struct {
	capacity int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrIllegalConfig
	}
	return &Window{capacity: capacity}, nil
}

func (w *Window) Cap() int { return w.capacity }

func (w *Window) Len() int { return 0 }

func (w *Window) Add(b byte) {}

func (w *Window) At(distance int) byte { return 0 }

func (w *Window) Preset(data []byte) {}
