package ontology

import (
	"errors"
	"time"
)

var (
	ErrInvalidRequest  = errors.New("invalid token request")
	ErrRequestTooLarge = errors.New("request can never fit in bucket")
	ErrTimeReversed    = errors.New("bucket time moved backwards")
)

// InsufficientError reports that the request is valid but cannot be served now.
type InsufficientError struct {
	Available  int64
	Requested  int64
	RetryAfter time.Duration
}

func (e *InsufficientError) Error() string {
	return "insufficient tokens"
}

func (e *InsufficientError) Is(target error) bool {
	_, ok := target.(*InsufficientError)
	return ok
}
