package swingdoor

import "errors"

var (
	ErrInvalidTolerance = errors.New("swingdoor: tolerance must be in [0, 4e9]")
	ErrValueOutOfRange  = errors.New("swingdoor: value absolute value exceeds 1e9")
	ErrTimeOutOfRange   = errors.New("swingdoor: timestamp absolute value exceeds 1e9")
	ErrDuplicateTime    = errors.New("swingdoor: timestamp equals previous sample")
	ErrTimeBeforePrev   = errors.New("swingdoor: timestamp before previous sample")
	ErrClosed           = errors.New("swingdoor: compressor already closed")
	ErrEmptyClose       = errors.New("swingdoor: closing empty stream")
)
