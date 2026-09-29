package window

import "errors"

var ErrCapacity = errors.New("window: capacity must be positive")

type Window struct{}

func New(capacity int) (*Window, error) { return nil, nil }
func (w *Window) Cap() int { return 0 }
func (w *Window) Len() int { return 0 }
func (w *Window) Put(b byte) {}
func (w *Window) At(distance int) byte { return 0 }
func (w *Window) Preset(data []byte) {}
