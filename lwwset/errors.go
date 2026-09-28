package lwwset

import "errors"

var (
	ErrNilReplica       = errors.New("lwwset: replica must not be nil")
	ErrInvalidReplicaID = errors.New("lwwset: replica id must be non-negative")
	ErrInvalidCapacity  = errors.New("lwwset: capacity limit must be positive")
	ErrEmptyElement     = errors.New("lwwset: element must not be empty string")
	ErrNonPositiveTime  = errors.New("lwwset: timestamp must be positive")
	ErrTooManyElements  = errors.New("lwwset: recorded element count exceeds capacity")
	ErrInvalidPosition  = errors.New("lwwset: merge position must be non-negative")
	ErrMergeSelf        = errors.New("lwwset: cannot merge a replica into itself")
)
