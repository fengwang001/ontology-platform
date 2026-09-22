package sparse

import "math"

// validate checks one vector: indices strictly ascending, no NaN.
// It returns the number of explicit zero entries. The vector is
// only read, never modified.
func validate(v Vector, which int) (zeros int, err error) {
	for i, e := range v {
		if math.IsNaN(e.Value) {
			return 0, &NaNError{Vector: which, Position: i}
		}
		if i > 0 && e.Index <= v[i-1].Index {
			return 0, &OrderError{
				Vector:   which,
				Position: i,
				Prev:     v[i-1].Index,
				Got:      e.Index,
			}
		}
		if e.Value == 0 {
			zeros++
		}
	}
	return zeros, nil
}

// validatePair validates both vectors and aggregates explicit zeros.
func validatePair(a, b Vector) (zeros int, err error) {
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
