package ontology

// valueLess orders two sort values. NaN never reaches here (such rows are
// rejected up front). +0.0 and -0.0 are treated as equal so they tie.
// Infinity participates normally, landing at the extreme ends.
func valueLess(a, b float64) bool {
	if a == 0 && b == 0 {
		return false
	}
	return a < b
}

// rowSorter sorts rows by (value, ID). Ties on value are broken by ID in
// ascending order regardless of dir; IDs are assumed unique within a
// partition, giving a deterministic total order independent of input order.
type rowSorter struct {
	rows  []Row
	dir   Direction
	count *comparisonCounter
}

func (s rowSorter) Len() int { return len(s.rows) }

func (s rowSorter) Swap(i, j int) {
	s.rows[i], s.rows[j] = s.rows[j], s.rows[i]
}

func (s rowSorter) Less(i, j int) bool {
	s.count.add(1)
	a, b := s.rows[i], s.rows[j]
	less := valueLess(a.SortValue, b.SortValue)
	greater := valueLess(b.SortValue, a.SortValue)
	if s.dir == Desc {
		less, greater = greater, less
	}
	if less {
		return true
	}
	if greater {
		return false
	}
	return a.ID < b.ID
}

// comparisonCounter counts value comparisons performed while sorting one
// partition. sort.Interface.Less calls are the only comparisons, so the
// count tracks sort cost (O(n log n)) rather than pair scanning.
type comparisonCounter struct {
	n int
}

func (c *comparisonCounter) add(d int) { c.n += d }

// Comparisons returns the number of comparisons observed so far.
func (c *comparisonCounter) Comparisons() int { return c.n }
