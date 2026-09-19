package ontology

import "math"

// better reports whether a outranks b under the composite ordering.
//
// The direction applies only to Score: Desc prefers larger scores, Asc
// prefers smaller ones. Tie breaking is independent of direction: equal
// scores are always ordered by ID ascending. NaN never participates in
// ranking (callers must reject it before comparing). +0.0 and -0.0 are
// treated as equal so the ID rule alone decides their order.
func better(dir Direction, a, b Element) bool {
	as := normZero(a.Score)
	bs := normZero(b.Score)
	if as != bs {
		if dir == Desc {
			return as > bs
		}
		return as < bs
	}
	return a.ID < b.ID
}

// worse is the inverse of better for the same composite ordering.
func worse(dir Direction, a, b Element) bool {
	return better(dir, b, a)
}

// normZero collapses -0.0 onto +0.0 so the sign bit cannot affect order.
func normZero(f float64) float64 {
	if f == 0 {
		return 0
	}
	return f
}

func isNaN(f float64) bool {
	return math.IsNaN(f)
}
