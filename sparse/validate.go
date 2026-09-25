package sparse

import "math"

// validate checks one vector: indices strictly increasing, no NaN
// values. Explicit zeros are legal and counted into stats. The vector
// is only read, never modified.
func validate(v Vector, which int, stats *Stats) error {
	var prev uint32
	for i, e := range v {
		if math.IsNaN(e.Value) {
			return &NaNError{Vector: which, Position: i}
		}
		if i > 0 && e.Index <= prev {
			return &OrderError{Vector: which, Position: i, Prev: prev, Cur: e.Index}
		}
		if e.Value == 0 {
			stats.ExplicitZeros++
		}
		prev = e.Index
	}
	return nil
}

// validateBoth validates a (vector 0) and b (vector 1), accumulating
// explicit-zero counts from both into one Stats.
func validateBoth(a, b Vector) (Stats, error) {
	var st Stats
	if err := validate(a, 0, &st); err != nil {
		return st, err
	}
	if err := validate(b, 1, &st); err != nil {
		return st, err
	}
	return st, nil
}
