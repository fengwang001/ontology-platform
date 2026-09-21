package ontology

import (
	"errors"
	"fmt"
)

// NaNError reports a NaN element. NaN is unequal to everything including
// itself, so it can never take part in a set operation.
type NaNError struct {
	Stream int // index of the offending stream
	Index  int // index of the NaN element within that stream
}

func (e *NaNError) Error() string {
	return fmt.Sprintf("ontology: stream %d element %d is NaN", e.Stream, e.Index)
}

// OrderError reports a stream that is not in non-decreasing order: the
// element at Index is strictly smaller than its predecessor.
type OrderError struct {
	Stream int // index of the offending stream
	Index  int // index of the element that breaks the ordering
}

func (e *OrderError) Error() string {
	return fmt.Sprintf("ontology: stream %d is not ascending at element %d", e.Stream, e.Index)
}

// ErrEmptyIntersect is returned by Intersect when called with zero
// streams: the intersection of an empty family is undefined.
var ErrEmptyIntersect = errors.New("ontology: intersection of zero streams is undefined")

// ErrEmptyDifference is returned by Difference when called with zero
// streams: there is no minuend stream.
var ErrEmptyDifference = errors.New("ontology: difference of zero streams is undefined")
