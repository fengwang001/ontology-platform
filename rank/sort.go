package rank

import "sort"

// naturalCompare is the default value ordering: the usual float64
// order, where +0.0 == -0.0 and +/-Inf are legal endpoints.
// It must never be called with NaN (NaN rows are rejected up front).
func naturalCompare(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// comparator decides the full row order inside one partition: by sort
// value (honoring direction), then by row ID ascending so that ties
// are deterministic and independent of input order.
type comparator struct {
	compareValues func(a, b float64) int
	descending    bool
}

func newComparator(opts Options) comparator {
	cmp := opts.CompareValues
	if cmp == nil {
		cmp = naturalCompare
	}
	return comparator{compareValues: cmp, descending: opts.Descending}
}

// less reports whether row a sorts before row b.
func (c comparator) less(a, b Row) bool {
	v := c.compareValues(a.Value, b.Value)
	if c.descending {
		v = -v
	}
	if v != 0 {
		return v < 0
	}
	// Ties on value: row ID ascending, never the input position.
	return a.ID < b.ID
}

// tied reports whether two values are equal under the comparator.
// Direction does not matter for equality.
func (c comparator) tied(a, b float64) bool {
	return c.compareValues(a, b) == 0
}

// sortRows sorts rows in place using the comparator. sort.SliceStable
// keeps the result fully deterministic even if two rows share both
// value and ID, and runs in O(n log n) comparisons.
func (c comparator) sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		return c.less(rows[i], rows[j])
	})
}
