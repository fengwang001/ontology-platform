package sourcemap

import "testing"

func TestComposeLargeSparseCoordinatesDoNotInflateOutput(t *testing.T) {
	const large uint64 = MaxCoordinate - 10
	m2 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: large, Segments: []Segment{
		mapped(large, 0, large, large),
	}}}}
	m1 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: large, Segments: []Segment{
		mapped(large, 0, large, large),
		unmapped(large + 1),
	}}}}

	got, err := Compose(m2, m1)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{{GeneratedLine: large, Segments: []Segment{
		mapped(large, 0, large, large),
		unmapped(large + 1),
	}}}
	logDecision(t, [2]Mapping{m2, m1}, got.Rows, want, "only actual M1 boundaries create events; absolute coordinates are never enumerated")
	if !reflectDeepEqualRows(got.Rows, want) {
		t.Fatalf("got %#v, want %#v", got.Rows, want)
	}
}

func BenchmarkLookupLogarithmic(b *testing.B) {
	const rows = 4096
	const segmentsPerRow = 4096
	mapping := Mapping{SourceCount: 1}
	for line := 0; line < rows; line++ {
		row := Row{GeneratedLine: uint64(line)}
		for column := 0; column < segmentsPerRow; column++ {
			row.Segments = append(row.Segments, mapped(uint64(column), 0, uint64(line), uint64(column)))
		}
		row.Segments = append(row.Segments, unmapped(uint64(segmentsPerRow)))
		mapping.Rows = append(mapping.Rows, row)
	}
	index, err := newIndexedMapping(mapping)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		line := uint64(i % rows)
		column := uint64((i / rows) % segmentsPerRow)
		if result := index.lookup(line, column); !result.Mapped {
			b.Fatal("expected mapped result")
		}
	}
}

func BenchmarkComposeLinearInOutputEvents(b *testing.B) {
	const rows = 256
	const segmentsPerRow = 256
	build := func(sourceColumnStep uint64) Mapping {
		mapping := Mapping{SourceCount: 1}
		for line := 0; line < rows; line++ {
			row := Row{GeneratedLine: uint64(line)}
			for column := 0; column < segmentsPerRow; column++ {
				row.Segments = append(row.Segments, mapped(
					uint64(column),
					0,
					uint64(line),
					uint64(column)*sourceColumnStep,
				))
			}
			row.Segments = append(row.Segments, unmapped(segmentsPerRow))
			mapping.Rows = append(mapping.Rows, row)
		}
		return mapping
	}

	m1 := build(1)
	m2 := build(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compose(m2, m1); err != nil {
			b.Fatal(err)
		}
	}
}

func reflectDeepEqualRows(a, b []Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].GeneratedLine != b[i].GeneratedLine || len(a[i].Segments) != len(b[i].Segments) {
			return false
		}
		for j := range a[i].Segments {
			if a[i].Segments[j] != b[i].Segments[j] {
				return false
			}
		}
	}
	return true
}
