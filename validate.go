package sparse

import "math"

// validate checks that v has strictly increasing indices and no NaN
// values, and counts explicit zero entries into stats.
func validate(v *Vector, which int, stats *Stats) error {
	var prev uint32
	for i, e := range v.Elems {
		if math.IsNaN(e.Value) {
			return &ValidationError{Vector: which, Position: i, Reason: "value is NaN"}
		}
		if i > 0 && e.Index <= prev {
			return &ValidationError{Vector: which, Position: i, Reason: "indices not strictly increasing"}
		}
		if e.Value == 0 {
			stats.ExplicitZeros++
		}
		prev = e.Index
	}
	return nil
}
