package frag

import "fmt"

var (
	ErrEmptyFragment = fmt.Errorf("frag: empty fragment data")
	ErrZeroTotal     = fmt.Errorf("frag: total length is zero")
	ErrOutOfRange    = fmt.Errorf("frag: fragment exceeds total length")
)

// ConflictError reports that an incoming fragment overlaps already
// received bytes with different content.
type ConflictError struct {
	Start int
	End   int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("frag: conflicting bytes in range [%d,%d)", e.Start, e.End)
}
