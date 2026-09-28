package hotcount

import "errors"

var (
	ErrInvalidConfig     = errors.New("hotcount: invalid config")
	ErrElementOutOfRange = errors.New("hotcount: element out of range")
	ErrNonPositiveCount  = errors.New("hotcount: count must be positive")
	ErrCountOverflow     = errors.New("hotcount: count overflow")
	ErrSelfCheck         = errors.New("hotcount: self check failed")
)
