package cal

import "errors"

var (
	ErrInvalid = errors.New("cal: invalid argument")
	ErrClock   = errors.New("cal: clock moved backwards")
	ErrPast    = errors.New("cal: holiday day is not strictly in the future")
)
