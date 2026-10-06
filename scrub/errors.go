package scrub

import "errors"

// 可区分的拒绝类错误，按校验次序由上至下排列。
var (
	ErrInvalid     = errors.New("scrub: invalid argument")
	ErrClockBack   = errors.New("scrub: clock moved backwards")
	ErrNotFound    = errors.New("scrub: block not found")
	ErrTooFrequent = errors.New("scrub: patrol too frequent")
)
