package record

import "errors"

var (
	// ErrShortFrame means the buffer does not contain a whole frame.
	ErrShortFrame = errors.New("record: short frame")
	// ErrFrameTooLarge means a frame length exceeds implementation limits.
	ErrFrameTooLarge = errors.New("record: frame too large")
)
