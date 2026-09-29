package topk

import "errors"

var (
	ErrNonPositiveK  = errors.New("topk: k must be positive")
	ErrKExceedsCap   = errors.New("topk: k exceeds capacity")
	ErrEmptyID       = errors.New("topk: element id must not be empty")
	ErrCapacityFull  = errors.New("topk: capacity reached for new element")
	ErrKExceedsLimit = errors.New("topk: requested k exceeds configured limit")
	ErrInvalidScore  = errors.New("topk: score must be a finite number")
)
