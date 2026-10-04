package exec

import "errors"

var (
	ErrInvalid      = errors.New("invalid argument")
	ErrNotFound     = errors.New("worker, operation or waiter not found")
	ErrExists       = errors.New("already exists")
	ErrState        = errors.New("state conflict")
	ErrStaleAttempt = errors.New("stale attempt")
	ErrNoSlot       = errors.New("no free slot")
)
