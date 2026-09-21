package ontology

import (
	"errors"
	"fmt"
)

// ErrNoStreams is returned by Intersect when called with zero streams:
// the intersection of an empty family of sets is undefined.
var ErrNoStreams = errors.New("ontology: intersection of zero streams is undefined")

// NaNError reports a NaN element. NaN never equals anything, not even
// itself, so it cannot take part in set operations.
type NaNError struct {
	Stream int // index of the offending stream
	Index  int // index of the NaN element within that stream
}

func (e NaNError) Error() string {
	return fmt.Sprintf("ontology: stream %d element %d is NaN", e.Stream, e.Index)
}

// UnsortedError reports a stream that is not in ascending order: the
// element at Index is strictly smaller than its predecessor. Equal
// adjacent elements are legal and never trigger this error.
type UnsortedError struct {
	Stream int     // index of the offending stream
	Index  int     // index of the out-of-order element
	Prev   float64 // previous element
	Cur    float64 // offending element, Cur < Prev
}

func (e UnsortedError) Error() string {
	return fmt.Sprintf("ontology: stream %d is not sorted: element %d (%v) < previous (%v)",
		e.Stream, e.Index, e.Cur, e.Prev)
}
