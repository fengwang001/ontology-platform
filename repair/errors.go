package repair

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockBack       = errors.New("clock moved backwards")
	ErrNotFound        = errors.New("ticket or contractor not found")
	ErrInvalidState    = errors.New("operation not allowed in current state")
	ErrNoCandidate     = errors.New("no available candidate and preemption is impossible")
	ErrPermission      = errors.New("permission denied")
)
