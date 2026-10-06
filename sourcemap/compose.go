package sourcemap

import "sort"

// Compose returns the canonical final-to-source mapping for M2 followed by M1.
func Compose(finalToIntermediate, intermediateToSource Mapping) (Mapping, error) {
	m1, err := newIndexedMapping(intermediateToSource)
	if err != nil {
		return Mapping{}, err
	}
	m2, err := newFinalToIntermediateMapping(finalToIntermediate)
	if err != nil {
		return Mapping{}, err
	}

	raw := Mapping{SourceCount: m1.sourceCount}
	raw.Rows = make([]Row, 0, len(m2.rows))
	for _, finalRow := range m2.rows {
		row := Row{GeneratedLine: finalRow.line}
		for segmentIndex, finalSegment := range finalRow.segments {
			endExclusive := MaxCoordinate + 1
			if segmentIndex+1 < len(finalRow.segments) {
				endExclusive = finalRow.segments[segmentIndex+1].GeneratedColumn
			}

			events, err := compositionEvents(m1, finalSegment, endExclusive)
			if err != nil {
				return Mapping{}, err
			}
			if err := appendCompositionSegments(&row, finalSegment, endExclusive, events); err != nil {
				return Mapping{}, err
			}
		}
		raw.Rows = append(raw.Rows, row)
	}

	return canonicalize(raw)
}

func newFinalToIntermediateMapping(m Mapping) (*IndexedMapping, error) {
	index, err := newIndexedMapping(m)
	if err != nil {
		return nil, err
	}
	if index.sourceCount != 1 {
		return nil, invalidArgument("final-to-intermediate mapping must declare exactly one source")
	}
	for _, row := range index.rows {
		for _, segment := range row.segments {
			if segment.Mapped && segment.SourceIndex != 0 {
				return nil, invalidArgument("final-to-intermediate segment source index must be zero")
			}
		}
	}
	return index, nil
}

type compositionEvent struct {
	column uint64
	result LookupResult
}

func compositionEvents(m1 *IndexedMapping, finalSegment Segment, endExclusive uint64) ([]compositionEvent, error) {
	if !finalSegment.Mapped {
		return []compositionEvent{{column: finalSegment.GeneratedColumn, result: LookupResult{}}}, nil
	}

	startResult := lookupIntermediate(m1, finalSegment.SourceLine, finalSegment.SourceColumn)
	events := []compositionEvent{{column: finalSegment.GeneratedColumn, result: startResult}}

	m1Segments := segmentsForLine(m1, finalSegment.SourceLine)
	firstBoundary := sort.Search(len(m1Segments), func(i int) bool {
		return m1Segments[i].GeneratedColumn > finalSegment.SourceColumn
	})

	for boundaryIndex := firstBoundary; boundaryIndex < len(m1Segments); boundaryIndex++ {
		intermediateColumn := m1Segments[boundaryIndex].GeneratedColumn
		if intermediateColumn > MaxCoordinate {
			break
		}
		finalColumn := finalSegment.GeneratedColumn +
			(intermediateColumn - finalSegment.SourceColumn)
		if finalColumn >= endExclusive {
			break
		}
		events = append(events, compositionEvent{
			column: finalColumn,
			result: lookupIntermediate(m1, finalSegment.SourceLine, intermediateColumn),
		})
	}

	if finalSegment.SourceColumn <= MaxCoordinate {
		offsetToOutOfDomain := MaxCoordinate + 1 - finalSegment.SourceColumn
		outOfDomainFinalColumn := finalSegment.GeneratedColumn + offsetToOutOfDomain
		if outOfDomainFinalColumn < endExclusive {
			events = append(events, compositionEvent{
				column: outOfDomainFinalColumn,
				result: LookupResult{},
			})
		}
	}

	return events, nil
}

func appendCompositionSegments(
	row *Row,
	finalSegment Segment,
	endExclusive uint64,
	events []compositionEvent,
) error {
	for eventIndex, event := range events {
		segmentEnd := endExclusive - 1
		if eventIndex+1 < len(events) {
			segmentEnd = events[eventIndex+1].column - 1
		}
		if err := checkResultThroughEnd(event.result, event.column, segmentEnd); err != nil {
			return err
		}

		segment := Segment{GeneratedColumn: event.column, Mapped: event.result.Mapped}
		if event.result.Mapped {
			segment.SourceIndex = event.result.SourceIndex
			segment.SourceLine = event.result.SourceLine
			segment.SourceColumn = event.result.SourceColumn
		}
		row.Segments = append(row.Segments, segment)
	}
	return nil
}

func checkResultThroughEnd(result LookupResult, startColumn, endColumn uint64) error {
	if result.Mapped && result.SourceColumn+(endColumn-startColumn) > MaxCoordinate {
		return positionOverflow("composed source column exceeds 10^9")
	}
	return nil
}

func segmentsForLine(m *IndexedMapping, line uint64) []Segment {
	rowIndex := sort.Search(len(m.rows), func(i int) bool {
		return m.rows[i].line >= line
	})
	if rowIndex == len(m.rows) || m.rows[rowIndex].line != line {
		return nil
	}
	return m.rows[rowIndex].segments
}

func lookupIntermediate(m *IndexedMapping, line, column uint64) LookupResult {
	if line > MaxCoordinate || column > MaxCoordinate {
		return LookupResult{}
	}
	return m.lookup(line, column)
}
