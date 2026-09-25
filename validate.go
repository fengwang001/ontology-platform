package sparse

import "math"

// validate checks that v is well-formed: indices strictly ascending and no
// NaN values. It returns the number of explicit zero-valued elements.
// The vector is never modified.
func validate(name string, v Vector) (zeros uint64, err error) {
	for i, e := range v {
		if math.IsNaN(e.Value) {
			return 0, nanErr(name, i)
		}
		if i > 0 && e.Index <= v[i-1].Index {
			return 0, sortErr(name, i, v[i-1].Index, e.Index)
		}
		if e.Value == 0 {
			zeros++
		}
	}
	return zeros, nil
}

// identical reports whether a and b hold exactly the same elements,
// compared bitwise on values.
func identical(a, b Vector) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Index != b[i].Index ||
			math.Float64bits(a[i].Value) != math.Float64bits(b[i].Value) {
			return false
		}
	}
	return true
}
