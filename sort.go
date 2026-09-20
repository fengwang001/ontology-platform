package ontology

import "sort"

// Result is the outcome of one Sort call.
type Result struct {
	// Rows holds the sorted rows in a new slice. The input slice and
	// the row maps themselves are never modified.
	Rows []map[string]any
	// Indices[i] is the original input index of Rows[i]. It makes
	// stability directly observable: rows equal on every key appear in
	// strictly increasing index order.
	Indices []int
	// Comparisons is the number of key-value comparisons this call
	// performed. Keys after the first decisive key are never compared,
	// so this count does not grow with the number of trailing keys.
	Comparisons int
	// NaNs is the number of NaN key values encountered. NaN is treated
	// exactly like a null and never participates in numeric comparison.
	NaNs int
}

// Sort stably sorts rows by the sorter's keys and returns a Result.
// It neither mutates the row maps nor reorders the input slice.
//
// Sort returns an *IncomparableError when two rows hold values of
// incompatible types for the same key.
func (s *Sorter) Sort(rows []map[string]any) (Result, error) {
	cells := make([][]cell, len(rows))
	nans := 0
	for i, row := range rows {
		rowCells := make([]cell, len(s.keys))
		for k, key := range s.keys {
			c, isNaN := extractCell(row, key.Field)
			if isNaN {
				nans++
			}
			rowCells[k] = c
		}
		cells[i] = rowCells
	}

	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}

	comparisons := 0
	var cmpErr error
	// SliceStable preserves the input-relative order of rows that
	// compare equal on every key; descending order is achieved by
	// negating value comparisons, never by reversing the output.
	sort.SliceStable(order, func(x, y int) bool {
		if cmpErr != nil {
			return false
		}
		less, err := s.less(cells[order[x]], cells[order[y]], &comparisons)
		if err != nil {
			cmpErr = err
			return false
		}
		return less
	})
	if cmpErr != nil {
		return Result{}, cmpErr
	}

	res := Result{
		Rows:        make([]map[string]any, len(rows)),
		Indices:     make([]int, len(rows)),
		Comparisons: comparisons,
		NaNs:        nans,
	}
	for i, idx := range order {
		res.Rows[i] = rows[idx]
		res.Indices[i] = idx
	}
	return res, nil
}

// less reports whether row a sorts before row b, counting every key
// comparison into counter. It short-circuits on the first decisive key.
func (s *Sorter) less(a, b []cell, counter *int) (bool, error) {
	for k, key := range s.keys {
		*counter++
		cmp, err := compareCells(a[k], b[k], key, k)
		if err != nil {
			return false, err
		}
		if cmp != 0 {
			return cmp < 0, nil
		}
	}
	return false, nil
}
