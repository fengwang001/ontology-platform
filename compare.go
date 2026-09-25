package ontology

import "math"

// Direction selects ascending or descending ordering of Value within a
// partition. Ties are always broken by ID ascending, in either direction.
type Direction int

const (
	Asc Direction = iota
	Desc
)

// compareValues orders two non-NaN float64 values. +0.0 and -0.0 compare
// equal so that they form a tie; ±Inf sort at the two ends naturally.
// Returns -1, 0, or 1.
func compareValues(a, b float64) int {
	if a == b {
		return 0
	}
	if a < b {
		return -1
	}
	return 1
}

func isNaN(v float64) bool {
	return math.IsNaN(v)
}
