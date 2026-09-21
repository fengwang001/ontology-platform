package reasm

import (
	"errors"
	"fmt"
)

// Distinguishable validation failures returned by Submit.
var (
	// ErrEmptyFragment is returned for a zero-length fragment payload.
	ErrEmptyFragment = errors.New("reasm: empty fragment")
	// ErrInvalidTotal is returned when total is zero or negative.
	ErrInvalidTotal = errors.New("reasm: total must be positive")
	// ErrOutOfRange is returned when a fragment reaches past total.
	ErrOutOfRange = errors.New("reasm: fragment extends beyond total")
)

// TotalMismatchError is returned when a fragment for a known message ID
// carries a total length different from the one first recorded.
type TotalMismatchError struct {
	Recorded int64 // total remembered for this message ID
	Got      int64 // total carried by the rejected fragment
}

func (e *TotalMismatchError) Error() string {
	return fmt.Sprintf("reasm: total mismatch: have %d, got %d", e.Recorded, e.Got)
}
