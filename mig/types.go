package mig

import "errors"

// Sentinel errors. Rejection order is: invalid argument -> crashed -> state.
var (
	ErrInvalidArg = errors.New("mig: invalid argument")
	ErrCrashed    = errors.New("mig: store is crashed")
	ErrDrained    = errors.New("mig: old store already drained")
	ErrNotDrained = errors.New("mig: old store not drained")
	ErrNotCrashed = errors.New("mig: store is not crashed")
)

// KV is a logical scanned entry in ascending logical-key order.
type KV struct {
	Key   int64
	Value int64
}
