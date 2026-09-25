package sparse

import (
	"fmt"
	"math"
)

// validate checks one vector and returns the number of explicit zeros.
//
// At every 1-based position it checks:
//  1. NaN value (NaN is ordered neither less nor greater than anything,
//     so it must be caught before any other comparison);
//  2. +/-Inf value (dot products involving it can become Inf/NaN);
//  3. strictly ascending index relative to the previous element.
//
// Explicit zero elements are legal and only counted.
func validate(v Vector, id VectorID) (zeros int, err error) {
	var prev uint32
	for i, e := range v {
		pos := i + 1
		switch {
		case math.IsNaN(e.Value):
			return 0, &Error{
				Reason:   ReasonNaN,
				Vector:   id,
				Position: pos,
				Msg: fmt.Sprintf("sparse: %s has NaN value at position %d (index %d)",
					id, pos, e.Index),
			}
		case math.IsInf(e.Value, 0):
			return 0, &Error{
				Reason:   ReasonInf,
				Vector:   id,
				Position: pos,
				Msg: fmt.Sprintf("sparse: %s has infinite value at position %d (index %d)",
					id, pos, e.Index),
			}
		}

		if e.Value == 0 {
			zeros++
		}

		if i > 0 && e.Index <= prev {
			kind := "equal"
			if e.Index < prev {
				kind = "decreasing"
			}
			return 0, &Error{
				Reason:   ReasonNonStrictOrder,
				Vector:   id,
				Position: pos,
				Msg: fmt.Sprintf("sparse: %s has %s index at position %d "+
					"(index %d follows index %d)",
					id, kind, pos, e.Index, prev),
			}
		}
		prev = e.Index
	}
	return zeros, nil
}

// validateBoth validates the two vectors and returns their combined
// explicit-zero count.
func validateBoth(a, b Vector) (int, error) {
	za, err := validate(a, VectorA)
	if err != nil {
		return 0, err
	}
	zb, err := validate(b, VectorB)
	if err != nil {
		return 0, err
	}
	return za + zb, nil
}
