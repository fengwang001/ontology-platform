// Package ontology provides sparse-vector dot product and cosine
// similarity computed with a single two-pointer merge, never
// expanding the sparse representation into a dense array.
package ontology

import "math"

// Vector is a sparse vector: Indices are strictly ascending dimension
// subscripts and Values holds the coefficient at each subscript.
// Explicit zero values are legal. The two slices must have equal length.
type Vector struct {
	Indices []uint32
	Values  []float64
}

// Stats reports observable facts about one computation.
type Stats struct {
	// Steps is the number of two-pointer merge iterations performed.
	Steps int
	// ExplicitZeros is the number of stored zero-valued elements seen
	// across both input vectors.
	ExplicitZeros int
}

// validate checks one vector: slice lengths must match, indices must be
// strictly ascending, and no stored value may be NaN. It returns the
// number of explicit zero-valued elements. The vector is not modified.
func validate(v Vector, which int) (int, error) {
	if len(v.Indices) != len(v.Values) {
		return 0, newError(KindLengthMismatch, which, -1,
			"Indices and Values have different lengths")
	}
	zeros := 0
	for i, val := range v.Values {
		if math.IsNaN(val) {
			return 0, newError(KindNaN, which, i, "value is NaN")
		}
		if val == 0 {
			zeros++
		}
		if i > 0 && v.Indices[i] <= v.Indices[i-1] {
			return 0, newError(KindNotSorted, which, i,
				"index is not strictly greater than the previous index")
		}
	}
	return zeros, nil
}

// validatePair validates both vectors and sums their explicit zeros.
func validatePair(a, b Vector) (int, error) {
	za, err := validate(a, 0)
	if err != nil {
		return 0, err
	}
	zb, err := validate(b, 1)
	if err != nil {
		return 0, err
	}
	return za + zb, nil
}
