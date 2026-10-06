package sourcemap

import (
	"sort"
)

func cloneMapping(m Mapping) Mapping {
	clone := Mapping{SourceCount: m.SourceCount}
	clone.Rows = make([]Row, len(m.Rows))
	for rowIndex, row := range m.Rows {
		clone.Rows[rowIndex] = Row{
			GeneratedLine: row.GeneratedLine,
			Segments:      append([]Segment(nil), row.Segments...),
		}
	}
	return clone
}

func validateMapping(m Mapping) error {
	seenRows := make(map[uint64]struct{}, len(m.Rows))
	for _, row := range m.Rows {
		if row.GeneratedLine > MaxCoordinate {
			return invalidArgument("generated line exceeds 10^9")
		}
		if _, exists := seenRows[row.GeneratedLine]; exists {
			return invalidArgument("duplicate generated line")
		}
		seenRows[row.GeneratedLine] = struct{}{}

		var previousColumn uint64
		for segmentIndex, segment := range row.Segments {
			if segment.GeneratedColumn > MaxCoordinate {
				return invalidArgument("generated column exceeds 10^9")
			}
			if segmentIndex > 0 && segment.GeneratedColumn <= previousColumn {
				return invalidArgument("generated columns must be strictly increasing")
			}
			previousColumn = segment.GeneratedColumn

			if segment.Mapped {
				if segment.SourceIndex >= m.SourceCount {
					return invalidArgument("source index is out of range")
				}
				if segment.SourceLine > MaxCoordinate || segment.SourceColumn > MaxCoordinate {
					return invalidArgument("source position exceeds 10^9")
				}
			}
		}
	}
	return nil
}

func canonicalize(m Mapping) (Mapping, error) {
	if err := validateMapping(m); err != nil {
		return Mapping{}, err
	}

	result := Mapping{SourceCount: m.SourceCount}
	result.Rows = make([]Row, 0, len(m.Rows))
	for _, row := range m.Rows {
		canonicalRow := Row{GeneratedLine: row.GeneratedLine}
		for _, segment := range row.Segments {
			candidate := segment
			if !candidate.Mapped {
				candidate.SourceIndex = 0
				candidate.SourceLine = 0
				candidate.SourceColumn = 0
			}

			if len(canonicalRow.Segments) == 0 {
				if candidate.GeneratedColumn > 0 {
					canonicalRow.Segments = append(canonicalRow.Segments, Segment{})
				}
				if candidate.GeneratedColumn == 0 && !candidate.Mapped {
					continue
				}
				canonicalRow.Segments = append(canonicalRow.Segments, candidate)
				continue
			}

			last := canonicalRow.Segments[len(canonicalRow.Segments)-1]
			if segmentsEquivalentAt(last, candidate.GeneratedColumn, candidate) {
				continue
			}
			canonicalRow.Segments = append(canonicalRow.Segments, candidate)
		}

		if len(canonicalRow.Segments) > 0 {
			if canonicalRow.Segments[0].GeneratedColumn == 0 && !canonicalRow.Segments[0].Mapped {
				canonicalRow.Segments = canonicalRow.Segments[1:]
			}
		}
		if len(canonicalRow.Segments) > 0 {
			result.Rows = append(result.Rows, canonicalRow)
		}
	}

	sort.Slice(result.Rows, func(i, j int) bool {
		return result.Rows[i].GeneratedLine < result.Rows[j].GeneratedLine
	})
	return result, nil
}

func segmentsEquivalentAt(previous Segment, column uint64, candidate Segment) bool {
	if previous.Mapped != candidate.Mapped {
		return false
	}
	if !previous.Mapped {
		return true
	}
	return previous.SourceIndex == candidate.SourceIndex &&
		previous.SourceLine == candidate.SourceLine &&
		previous.SourceColumn+(column-previous.GeneratedColumn) == candidate.SourceColumn
}

// NewIndexedMapping validates and canonicalizes a mapping once.
func NewIndexedMapping(m Mapping) (*IndexedMapping, error) {
	canonical, err := canonicalize(m)
	if err != nil {
		return nil, err
	}
	index := &IndexedMapping{sourceCount: canonical.SourceCount}
	index.rows = make([]indexRow, len(canonical.Rows))
	for i, row := range canonical.Rows {
		index.rows[i] = indexRow{line: row.GeneratedLine, segments: row.Segments}
	}
	return index, nil
}

func newIndexedMapping(m Mapping) (*IndexedMapping, error) {
	return NewIndexedMapping(m)
}

func (m *IndexedMapping) toMapping() Mapping {
	result := Mapping{SourceCount: m.sourceCount}
	result.Rows = make([]Row, len(m.rows))
	for i, row := range m.rows {
		result.Rows[i] = Row{GeneratedLine: row.line, Segments: append([]Segment(nil), row.segments...)}
	}
	return result
}
