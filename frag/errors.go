package frag

import "fmt"

// ConflictError reports that an incoming fragment overlaps already stored
// bytes but disagrees with them. [Start, End) is the exact half-open byte
// range on which the contents differ.
type ConflictError struct {
	Start int
	End   int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("frag: conflicting bytes in range [%d,%d)", e.Start, e.End)
}
