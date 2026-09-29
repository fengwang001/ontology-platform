package dec

import (
	"errors"

	"ontology/window"
)

var ErrZeroLimit = errors.New("dec: output limit must be > 0")

type Reader struct{ win *window.Window }

func NewReader(windowCap, outputLimit int) (*Reader, error) {
	if windowCap <= 0 {
		return nil, window.ErrZeroCap
	}
	if outputLimit <= 0 {
		return nil, ErrZeroLimit
	}
	win, err := window.New(windowCap)
	if err != nil {
		return nil, err
	}
	return &Reader{win: win}, nil
}
