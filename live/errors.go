package live

import "errors"

var (
	ErrClockRollback = errors.New("live: clock rollback")
	ErrAlreadyEnded  = errors.New("live: stream already ended")
	ErrInvalidKind   = errors.New("live: invalid kind")
)
