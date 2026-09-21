package ontology

import (
	"errors"
	"fmt"
)

// ErrNoStreams is returned by operations whose result is undefined for
// zero input streams (Intersect and Difference). Union of zero streams
// is defined as the empty set and does not produce this error.
var ErrNoStreams = errors.New("ontology: operation is undefined for zero streams")

// NaNError reports that a stream element is NaN. NaN is not equal to any
// value, including itself, so it cannot participate in set operations.
type NaNError struct {
	Stream int // index of the offending stream
	Index  int // index of the NaN element within that stream
}

func (e *NaNError) Error() string {
	return fmt.Sprintf("ontology: stream %d element %d is NaN", e.Stream, e.Index)
}

// UnsortedError reports that a stream is not in non-decreasing order.
// Index is the position of the first element that is strictly smaller
// than its predecessor. Equal adjacent elements are allowed.
type UnsortedError struct {
	Stream int // index of the offending stream
	Index  int // position of the first out-of-order element
}

func (e *UnsortedError) Error() string {
	return fmt.Sprintf("ontology: stream %d is not sorted: element %d is less than its predecessor", e.Stream, e.Index)
}
