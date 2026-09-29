package closure

import "errors"

var (
	ErrInvalidTargetLag  = errors.New("closure: target lag must not be negative")
	ErrInvalidClock      = errors.New("closure: injected clock is required")
	ErrInvalidReplicas   = errors.New("closure: replica set is invalid")
	ErrNegativeTimestamp = errors.New("closure: timestamp must not be negative")
	ErrReplicaNotFound   = errors.New("closure: replica not found")
	ErrInvalidMessage    = errors.New("closure: message is invalid")
	ErrNotClosed         = errors.New("closure: timestamp is not closed")
	ErrNotCaughtUp       = errors.New("closure: replica has not caught up to the publication")
)
