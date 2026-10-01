package vegas

import "errors"

var (
	ErrInvalidConfig = errors.New("vegas: invalid config")
	ErrInvalidResult = errors.New("vegas: invalid result")
	ErrInvalidTime   = errors.New("vegas: invalid time")
	ErrClockRewind   = errors.New("vegas: clock rewind")
	ErrAtLimit       = errors.New("vegas: at limit")
	ErrTokenTimedOut = errors.New("vegas: token timed out")
	ErrInvalidToken  = errors.New("vegas: invalid token")
	ErrInvalidRTT    = errors.New("vegas: invalid rtt")
)
