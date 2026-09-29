package versioned

import (
	"errors"
	"fmt"
)

// Sentinel errors are the distinct, distinguishable rejection reasons.
// Use errors.Is to classify a *BatchError's underlying reason.
var (
	ErrInvalidRetention  = errors.New("versioned: retention must be non-negative")
	ErrInvalidBatchLimit = errors.New("versioned: max batch size must be positive")
	ErrBatchTooLarge     = errors.New("versioned: batch entry count exceeds limit")
	ErrNilEvent          = errors.New("versioned: event is nil")
	ErrEmptyKey          = errors.New("versioned: event key is empty")
	ErrInvalidVersion    = errors.New("versioned: event version must be strictly positive")
	ErrInvalidOp         = errors.New("versioned: event op is neither write nor delete")
	ErrNilValue          = errors.New("versioned: write event value is nil")
	ErrDuplicateKey      = errors.New("versioned: duplicate key within one batch")
)

// BatchError reports a whole-batch rejection. Index is the offending event
// index, or -1 for a batch-level failure. Err is one of the sentinel errors.
type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string {
	if e.Index < 0 {
		return e.Err.Error()
	}
	return fmt.Sprintf("versioned: event %d rejected: %v", e.Index, e.Err)
}

// Unwrap exposes the sentinel reason so errors.Is works.
func (e *BatchError) Unwrap() error { return e.Err }

func batchErrf(index int, reason error, format string, args ...any) *BatchError {
	return &BatchError{Index: index, Err: fmt.Errorf("%w: %s", reason, fmt.Sprintf(format, args...))}
}
