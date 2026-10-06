package sourcemap

import (
	"math/rand"
	"strings"
	"testing"
)

func TestRandomComposeMatchesNaiveColumnEnumeration(t *testing.T) {
	const cases = 40
	const rows = 6
	const width = 24

	for seed := int64(1); seed <= cases; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m1 := randomMapping(rng, rows, width, 3, false)
		m2 := randomMapping(rng, rows, width, 1, true)

		composed, err := Compose(m2, m1)
		if err != nil {
			t.Fatalf("seed=%d composition: %v", seed, err)
		}
		composedIndex, err := newIndexedMapping(composed)
		if err != nil {
			t.Fatalf("seed=%d composed index: %v", seed, err)
		}
		m1Index, err := newIndexedMapping(m1)
		if err != nil {
			t.Fatalf("seed=%d m1 index: %v", seed, err)
		}
		m2Index, err := newIndexedMapping(m2)
		if err != nil {
			t.Fatalf("seed=%d m2 index: %v", seed, err)
		}

		for line := uint64(0); line < rows; line++ {
			for column := uint64(0); column < width; column++ {
				naive := LookupResult{}
				intermediate := m2Index.lookup(line, column)
				if intermediate.Mapped {
					naive = m1Index.lookup(intermediate.SourceLine, intermediate.SourceColumn)
				}
				got := composedIndex.lookup(line, column)
				if got != naive {
					t.Logf("seed=%d\ninput_m1=%#v\ninput_m2=%#v\noutput=%#v\ndecision=mismatch at line=%d column=%d", seed, m1, m2, composed, line, column)
					t.Fatalf("seed=%d line=%d column=%d got=%#v naive=%#v", seed, line, column, got, naive)
				}
			}
		}
		t.Logf("seed=%d\ninput_m1=%s\ninput_m2=%s\noutput=%s\ndecision=all %d cells equal M1(M2(cell))",
			seed, compactMapping(m1), compactMapping(m2), compactMapping(composed), rows*width)
	}
}

func segmentCount(m Mapping) int {
	total := 0
	for _, row := range m.Rows {
		total += len(row.Segments)
	}
	return total
}

func compactMapping(m Mapping) string {
	var builder strings.Builder
	builder.WriteString("{sources=")
	builder.WriteString(itoa(int64(m.SourceCount)))
	builder.WriteString(" rows=[")
	for rowIndex, row := range m.Rows {
		if rowIndex > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(itoa(int64(row.GeneratedLine)))
		builder.WriteByte(':')
		for segmentIndex, segment := range row.Segments {
			if segmentIndex > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(itoa(int64(segment.GeneratedColumn)))
			if segment.Mapped {
				builder.WriteString(">(")
				builder.WriteString(itoa(int64(segment.SourceIndex)))
				builder.WriteByte(',')
				builder.WriteString(itoa(int64(segment.SourceLine)))
				builder.WriteByte(',')
				builder.WriteString(itoa(int64(segment.SourceColumn)))
				builder.WriteByte(')')
			} else {
				builder.WriteString(">x")
			}
		}
	}
	builder.WriteString("]}")
	return builder.String()
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

func randomMapping(rng *rand.Rand, rows, width, sourceCount uint64, finalToIntermediate bool) Mapping {
	mapping := Mapping{SourceCount: sourceCount}
	for line := uint64(0); line < rows; line++ {
		row := Row{GeneratedLine: line}
		var previous Segment
		for column := uint64(0); column < width; column++ {
			segment := Segment{GeneratedColumn: column}
			if rng.Intn(10) >= 3 {
				segment.Mapped = true
				segment.SourceIndex = uint64(rng.Intn(int(sourceCount)))
				segment.SourceLine = uint64(rng.Intn(int(rows)))
				segment.SourceColumn = uint64(rng.Intn(int(width)))
				if finalToIntermediate {
					segment.SourceIndex = 0
				}
			}
			if column == 0 || segment != previous {
				row.Segments = append(row.Segments, segment)
			}
			previous = segment
		}
		row.Segments = append(row.Segments, Segment{GeneratedColumn: width})
		mapping.Rows = append(mapping.Rows, row)
	}
	return mapping
}
