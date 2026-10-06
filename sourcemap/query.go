package sourcemap

import "sort"

// Lookup returns the source position without scanning rows or segments.
func (m *IndexedMapping) Lookup(line, column uint64) (LookupResult, error) {
	if line > MaxCoordinate || column > MaxCoordinate {
		return LookupResult{}, invalidArgument("query position exceeds 10^9")
	}
	return m.lookup(line, column), nil
}

func (m *IndexedMapping) lookup(line, column uint64) LookupResult {
	rowIndex := sort.Search(len(m.rows), func(i int) bool {
		return m.rows[i].line >= line
	})
	if rowIndex == len(m.rows) || m.rows[rowIndex].line != line {
		return LookupResult{}
	}

	segments := m.rows[rowIndex].segments
	segmentIndex := sort.Search(len(segments), func(i int) bool {
		return segments[i].GeneratedColumn > column
	}) - 1
	if segmentIndex < 0 || !segments[segmentIndex].Mapped {
		return LookupResult{}
	}

	segment := segments[segmentIndex]
	return LookupResult{
		Mapped: true,
		SourcePosition: SourcePosition{
			SourceIndex:  segment.SourceIndex,
			SourceLine:   segment.SourceLine,
			SourceColumn: segment.SourceColumn + (column - segment.GeneratedColumn),
		},
	}
}

func (m *Mapping) Lookup(line, column uint64) (LookupResult, error) {
	if line > MaxCoordinate || column > MaxCoordinate {
		return LookupResult{}, invalidArgument("query position exceeds 10^9")
	}
	index, err := newIndexedMapping(*m)
	if err != nil {
		return LookupResult{}, err
	}
	return index.Lookup(line, column)
}
