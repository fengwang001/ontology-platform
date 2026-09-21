package streamset

import (
	"errors"
	"fmt"
)

// ErrEmptyIntersection is returned by Intersect (and Compute with
// OpIntersect) when called with zero streams: the intersection of an
// empty family of sets is undefined.
var ErrEmptyIntersection = errors.New("streamset: intersection of zero streams is undefined")

// NaNError reports a NaN element in an input stream. NaN is not equal to
// any value, including itself, so it cannot participate in set operations.
type NaNError struct {
	Stream int // index of the offending stream
	Index  int // index of the NaN element within that stream
}

func (e *NaNError) Error() string {
	return fmt.Sprintf("streamset: stream %d element %d is NaN", e.Stream, e.Index)
}

// OrderError reports a stream that is not in ascending order: the element
// at Index is strictly smaller than its predecessor. Equal adjacent
// elements are allowed and never trigger this error.
type OrderError struct {
	Stream int // index of the offending stream
	Index  int // index of the later element of the decreasing pair
}

func (e *OrderError) Error() string {
	return fmt.Sprintf("streamset: stream %d is not ascending at element %d", e.Stream, e.Index)
}
