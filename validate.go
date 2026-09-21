package ontology

import "math"

// validate checks every stream for NaN elements and for strict decreases.
// It never modifies the input. The first violation found is returned;
// streams are scanned in order, elements left to right.
//
// Notes on the equality contract enforced here:
//   - NaN fails every comparison, so it is rejected outright before any
//     ordering check can be silently skipped by a false comparison.
//   - +0.0 and -0.0 compare equal under ">", so a stream containing both
//     in either order is still considered sorted.
//   - +Inf and -Inf are ordinary ordered values and pass validation.
func validate(streams [][]float64) error {
	for s, stream := range streams {
		for i, v := range stream {
			if math.IsNaN(v) {
				return &NaNError{Stream: s, Index: i}
			}
			if i > 0 && stream[i-1] > v {
				return &UnsortedError{Stream: s, Index: i}
			}
		}
	}
	return nil
}
