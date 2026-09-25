package ontology

import "sort"

// indexedRow is a local copy of an accepted row so ranking never mutates the
// caller's Row values.
type indexedRow struct {
	partition string
	value     float64
	id        string
}

// sortPartition sorts by value in the requested direction; tied values always
// break by ascending row ID (never by input position or map order).
func sortPartition(rows []indexedRow, order Order, cmp *comparisonCounter) {
	sort.Slice(rows, func(i, j int) bool {
		sign := cmp.compare(rows[i].value, rows[j].value)
		if order == Desc {
			sign = -sign
		}
		if sign != 0 {
			return sign < 0
		}
		return rows[i].id < rows[j].id
	})
}
