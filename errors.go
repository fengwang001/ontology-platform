package ontology

import (
	"errors"
	"fmt"
)

// ErrNoStreams is returned by Intersect when called with zero streams:
// the intersection of an empty family of sets is undefined here.
var ErrNoStreams = errors.New("ontology: intersection of zero streams is undefined")

// NaNError reports a NaN element in an input stream. NaN is not equal
// to any value, including itself, so it cannot take part in set
// operations.
type NaNError struct {
	Stream int // index of the stream containing the NaN
	Index  int // index of the NaN element within that stream
}

func (e *NaNError) Error() string {
	return fmt.Sprintf("ontology: stream %d element %d is NaN", e.Stream, e.Index)
}

// OrderError reports a stream that is not sorted in non-decreasing
// order: the element at Index is strictly smaller than its predecessor.
type OrderViolation struct {
	Stream int     // index of the offending stream
	Index  int     // index of the element that breaks the ordering
	Prev   float64 // element at Index-1
	Cur    float64 // element at Index, Cur < Prev
}

func (e *OrderViolation) Error() string {
	return fmt.Sprintf("ontology: stream %d is not sorted: element %d (%v) is less than element %d (%v)",
		e.Stream, e.Index, e.Cur, e.Index-1, e.Prev)
}

// validateStream checks that s contains no NaN and is non-decreasing.
func validateStream(i int, s []float64) error {
	for j, v := range s {
		if v != v {
			return &NaNError{Stream: i, Index: j}
		}
		if j > 0 && s[j-1] > v {
			return &OrderViolation{Stream: i, Index: j, Prev: s[j-1], Cur: v}
		}
	}
	return nil
}
