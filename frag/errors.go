package frag

import (
	"errors"
	"fmt"
)

// Distinguishable validation failures returned by Set.Add and NewSet.
var (
	// ErrEmptyData is returned for a fragment carrying no payload bytes.
	ErrEmptyData = errors.New("frag: empty fragment data")
	// ErrZeroTotal is returned when the declared message length is not positive.
	ErrZeroTotal = errors.New("frag: total length must be positive")
	// ErrOutOfBounds is returned when offset+len(data) exceeds total,
	// or the offset is negative.
	ErrOutOfBounds = errors.New("frag: fragment out of bounds")
)

// ConflictError reports that an incoming fragment overlaps already-received
// bytes with different content. Start and End (exclusive) delimit the exact
// byte span whose content differs.
type ConflictError struct {
	Start int
	End   int
}

// Error implements the error interface.
func (e *ConflictError) Error() string {
	return fmt.Sprintf("frag: conflicting bytes in [%d,%d)", e.Start, e.End)
}
