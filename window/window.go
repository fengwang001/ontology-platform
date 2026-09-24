package window

import "errors"

var ErrBadConfig = errors.New("window: capacity must be positive")

type Window struct {
	buf []byte
	n   int
}

func New(capacity int) *Window { return &Window{} }

func (w *Window) Write(p []byte) {}

func (w *Window) Len() int { return 0 }

func (w *Window) Cap() int { return 0 }

func (w *Window) At(distance int) byte { return 0 }

func (w *Window) SliceBack(distance, length int) []byte { return nil }
