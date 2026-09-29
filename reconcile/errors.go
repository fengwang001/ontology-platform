package reconcile

import "errors"

var (
	ErrInvalidFanout = errors.New("reconcile: fanout must be >= 2")
	ErrKeySpaceSize  = errors.New("reconcile: key space size must be >= 1")
	ErrKeyOutOfRange = errors.New("reconcile: key out of range")
	ErrTooManyKeys   = errors.New("reconcile: key count exceeds key space size")
	ErrInvalidValue  = errors.New("reconcile: value must not be nil")
	ErrNilReplica    = errors.New("reconcile: replica must not be nil")
	ErrShapeMismatch = errors.New("reconcile: replicas have different shapes (key space size or fanout)")
)
