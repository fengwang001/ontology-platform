package ontology

// compareVectors compares version vectors by component order: it returns the
// index of the first component where a and b differ, along with its sign
// (-1 if a < b there, +1 if a > b). Equal vectors return (-1, 0). Vectors of
// differing length are compared over their shared prefix, then length acts as
// a tie breaker; every vector the store uses has the same length anyway.
func compareVectors(a, b []int64) (firstDiff int, sign int) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		switch {
		case a[i] < b[i]:
			return i, -1
		case a[i] > b[i]:
			return i, 1
		}
	}
	switch {
	case len(a) < len(b):
		return n, -1
	case len(a) > len(b):
		return n, 1
	default:
		return -1, 0
	}
}

// lexicographicallyGreater reports whether a is strictly newer than b under the
// component-order lexicographic rule.
func lexicographicallyGreater(a, b []int64) bool {
	_, sign := compareVectors(a, b)
	return sign > 0
}

// componentWiseMax raises clock to the component-wise maximum with v.
func componentWiseMax(clock, v []int64) {
	for i, x := range v {
		if x > clock[i] {
			clock[i] = x
		}
	}
}

// dominatedBy reports whether every component of v is <= the corresponding
// component of stable (reclamation safety test, not lexicographic).
func dominatedBy(v, stable []int64) bool {
	for i, x := range v {
		if x > stable[i] {
			return false
		}
	}
	return true
}
