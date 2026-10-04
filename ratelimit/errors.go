package ratelimit

import "errors"

var (
	ErrInvalidArg   = errors.New("ratelimit: invalid argument")
	ErrInvalidTime  = errors.New("ratelimit: invalid time")
	ErrClockRewind  = errors.New("ratelimit: clock rewind")
	ErrNoRoute      = errors.New("ratelimit: route not found")
	ErrNoTier       = errors.New("ratelimit: no tier registered")
	ErrForbidden    = errors.New("ratelimit: forbidden")
	ErrNeverAllowed = errors.New("ratelimit: request cost exceeds burst, never allowed")
	ErrTableFull    = errors.New("ratelimit: subject table full")
	ErrRateLimited  = errors.New("ratelimit: rate limited")
	ErrDuplicate    = errors.New("ratelimit: duplicate")
)
