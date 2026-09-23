package match

import (
	"errors"

	"ontology/window"
)

var (
	ErrInvalidWindow     = errors.New("match: window must be positive")
	ErrInvalidChainLimit = errors.New("match: chain limit must be positive")
)

type Finder struct {
	win *window.Window
}

func New(win *window.Window, chainLimit int) (*Finder, error) {
	return nil, nil
}

func (f *Finder) AddByte(b byte) {}

func (f *Finder) Add(p []byte) {}

func (f *Finder) Find(pending []byte, maxLength int) (distance, length int) { return 0, 0 }

func (f *Finder) Candidates() int64 { return 0 }
