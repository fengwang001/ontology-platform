package rank

import "math"

// valueEqual reports whether two sort values belong to the same tie group.
// NaN never equals anything (NaN rows are rejected before sorting), and
// the two floating-point zeros +0.0 and -0.0 are considered equal.
func valueEqual(a, b float64) bool {
	if a == 0 && b == 0 {
		return true
	}
	return a == b
}

// isNaN is an explicit check kept separate from call sites so the NaN
// rejection rule lives in one place.
func isNaN(v float64) bool {
	return math.IsNaN(v)
}

// comparator builds the less function used by sort.Slice. Rows are ordered
// by SortValue according to dir; ties are always broken by ID ascending,
// regardless of direction (descending must not reverse the tie order).
// Every pair comparison increments *count, which lets callers bound the
// comparison cost at O(n log n).
func comparator(rows []Row, dir Direction, count *int64) func(i, j int) bool {
	return func(i, j int) bool {
		*count++
		x, y := rows[i], rows[j]
		if !valueEqual(x.SortValue, y.SortValue) {
			if dir == Desc {
				return x.SortValue > y.SortValue
			}
			return x.SortValue < y.SortValue
		}
		return x.ID < y.ID
	}
}
