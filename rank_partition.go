package ontology

import "sort"

// valueCmp compares float64 sort keys. +0.0 and -0.0 compare equal, so they
// form one tie group. NaN never reaches here (such rows are rejected early).
func valueCmp(a, b float64) int {
	if a == b {
		// Covers +0.0 == -0.0 as well as Inf == Inf.
		return 0
	}
	if a < b {
		return -1
	}
	return 1
}

// baseLess orders by value in the requested direction and breaks ties by
// ascending ID. Descending reverses only the value comparison; the tie-break
// stays ID-ascending, which is why output is never a plain reversal.
func baseLess(dir Direction) lessFunc {
	return func(a, b sortedRow) bool {
		cmp := valueCmp(a.value, b.value)
		if dir == Descending {
			cmp = -cmp
		}
		if cmp != 0 {
			return cmp < 0
		}
		return a.id < b.id
	}
}

type sortableRows struct {
	rows []sortedRow
	less lessFunc
}

func (s sortableRows) Len() int           { return len(s.rows) }
func (s sortableRows) Less(i, j int) bool { return s.less(s.rows[i], s.rows[j]) }
func (s sortableRows) Swap(i, j int)      { s.rows[i], s.rows[j] = s.rows[j], s.rows[i] }

// rankPartition sorts one partition's copy and attaches the three columns.
func rankPartition(rows []sortedRow, dir Direction, injectLess func(counter *int) lessFunc) []RankedRow {
	counter := 0
	less := baseLess(dir)
	if injectLess != nil {
		less = injectLess(&counter)
	}

	// sort.Sort is pattern-aware quicksort with heapsort fallback:
	// O(n log n) comparisons with no quadratic worst case.
	sort.Sort(sortableRows{rows: rows, less: less})

	out := make([]RankedRow, 0, len(rows))
	rank := 1
	dense := 1
	for i := range rows {
		if i > 0 && valueCmp(rows[i-1].value, rows[i].value) != 0 {
			// New distinct value: RANK is this row's 1-based position
			// (gaps after ties), DENSE_RANK advances by exactly one.
			rank = i + 1
			dense++
		}
		out = append(out, RankedRow{
			Partition: rows[i].partition,
			Value:     rows[i].value,
			ID:        rows[i].id,
			RowNumber: i + 1,
			Rank:      rank,
			DenseRank: dense,
		})
	}
	return out
}
