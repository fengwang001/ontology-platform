package bptree

import (
	"errors"
	"fmt"
)

// Sentinel errors.
var (
	ErrBadLeafCapacity   = errors.New("bptree: leaf capacity C must be >= 2")
	ErrBadInternalFanout = errors.New("bptree: internal fanout B must be >= 3")
	ErrBadPercentage     = errors.New("bptree: fill percentage p must be in 1..100")
	ErrAlreadyFinished   = errors.New("bptree: loader already finished")
	ErrEmptyKey          = errors.New("bptree: key must not be empty")
	ErrGetBeforeFinish   = errors.New("bptree: Get called before Finish")
)

// NonMonotonicError reports a key that is not strictly greater than the
// largest accepted key. Index is the 0-based position the rejected key would
// have occupied in the accepted key stream.
type NonMonotonicError struct {
	Key      string
	Index    int
	Previous string
}

func (e *NonMonotonicError) Error() string {
	return fmt.Sprintf("bptree: key %q at index %d is not greater than previous key %q",
		e.Key, e.Index, e.Previous)
}

// Is matches only *NonMonotonicError so that errors.Is(err, ErrNotIncreasing)
// works while errors.As still exposes the offending key and index.
func (e *NonMonotonicError) Is(target error) bool {
	_, ok := target.(*NonMonotonicError)
	return ok
}

// ErrNotIncreasing is the sentinel matched by NonMonotonicError.
var ErrNotIncreasing = (*NonMonotonicError)(nil)

var _ error = (*NonMonotonicError)(nil)
