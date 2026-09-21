package sparsevec

import "math"

// validate checks that v is strictly ascending by index and NaN-free,
// and counts explicit zero-valued elements. It never mutates v.
func validate(which int, v Vector) (zeros int, err error) {
	for i, e := range v {
		if i > 0 && e.Index <= v[i-1].Index {
			return 0, notSortedErr(which, i, v[i-1].Index, e.Index)
		}
		if math.IsNaN(e.Value) {
			return 0, nanErr(which, i)
		}
		if e.Value == 0 {
			zeros++
		}
	}
	return zeros, nil
}
