package sourcemap

import (
	"reflect"
	"sync"
	"testing"
)

func mapped(column, source, line, sourceColumn uint64) Segment {
	return Segment{
		GeneratedColumn: column,
		Mapped:          true,
		SourceIndex:     source,
		SourceLine:      line,
		SourceColumn:    sourceColumn,
	}
}

func unmapped(column uint64) Segment {
	return Segment{GeneratedColumn: column}
}

func logDecision(t *testing.T, input, got, want any, reason string) {
	t.Helper()
	t.Logf("input=%#v\noutput=%#v\nexpected=%#v\ndecision=%s", input, got, want, reason)
}

func segmentStarts(m Mapping) []uint64 {
	var starts []uint64
	for _, row := range m.Rows {
		for _, segment := range row.Segments {
			starts = append(starts, segment.GeneratedColumn)
		}
	}
	return starts
}

func TestComposeSplitsAtM1Boundaries(t *testing.T) {
	m2 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 1, Segments: []Segment{mapped(0, 0, 7, 1)}}}}
	m1 := Mapping{SourceCount: 2, Rows: []Row{{GeneratedLine: 7, Segments: []Segment{
		mapped(0, 0, 10, 100),
		mapped(2, 1, 20, 200),
		mapped(5, 0, 30, 300),
		unmapped(11),
	}}}}

	got, err := Compose(m2, m1)
	wantRows := []Row{{GeneratedLine: 1, Segments: []Segment{
		mapped(0, 0, 10, 101),
		mapped(1, 1, 20, 200),
		mapped(4, 0, 30, 300),
		unmapped(10),
	}}}
	logDecision(t, [2]Mapping{m2, m1}, got.Rows, wantRows, "M1 boundaries 2 and 5 become final columns 1 and 4")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Rows, wantRows) {
		t.Fatalf("got %#v, want %#v", got.Rows, wantRows)
	}
}

func TestComposeSplitsInfiniteFinalSegmentMultipleTimes(t *testing.T) {
	m2 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{mapped(10, 0, 0, 4)}}}}
	m1 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 0),
		mapped(5, 0, 1, 50),
		mapped(7, 0, 2, 70),
		mapped(9, 0, 3, 90),
		mapped(11, 0, 4, 110),
		unmapped(13),
	}}}}

	got, err := Compose(m2, m1)
	wantStarts := []uint64{10, 11, 13, 15, 17, 19}
	logDecision(t, [2]Mapping{m2, m1}, segmentStarts(got), wantStarts, "the last M2 segment crosses each later M1 boundary")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(segmentStarts(got), wantStarts) {
		t.Fatalf("got %#v, want %#v", segmentStarts(got), wantStarts)
	}
}

func TestComposeDoesNotCrossNextM2SegmentStart(t *testing.T) {
	m2 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 5, 0),
		mapped(8, 0, 9, 0),
	}}}}
	m1 := Mapping{SourceCount: 1, Rows: []Row{
		{GeneratedLine: 5, Segments: []Segment{mapped(0, 0, 0, 0), mapped(5, 0, 6, 600), unmapped(8)}},
		{GeneratedLine: 9, Segments: []Segment{mapped(0, 0, 7, 700), unmapped(10)}},
	}}

	got, err := Compose(m2, m1)
	wantRows := []Row{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 0),
		mapped(5, 0, 6, 600),
		mapped(8, 0, 7, 700),
		unmapped(18),
	}}}
	logDecision(t, [2]Mapping{m2, m1}, got.Rows, wantRows, "a split cannot pass the next M2 segment start")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Rows, wantRows) {
		t.Fatalf("got %#v, want %#v", got.Rows, wantRows)
	}
}

func TestComposePreservesExplicitUnmappedFromM1(t *testing.T) {
	m2 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, 0)}}}}
	m1 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 0),
		unmapped(3),
		mapped(5, 0, 2, 20),
		unmapped(8),
	}}}}

	got, err := Compose(m2, m1)
	wantRows := []Row{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 0),
		unmapped(3),
		mapped(5, 0, 2, 20),
		unmapped(8),
	}}}
	logDecision(t, [2]Mapping{m2, m1}, got.Rows, wantRows, "M1 unmapped regions become explicit result unmapped segments")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Rows, wantRows) {
		t.Fatalf("got %#v, want %#v", got.Rows, wantRows)
	}
}

func TestLookupEqualityAndNoCrossRowBacktrack(t *testing.T) {
	mapping := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 4, Segments: []Segment{
		unmapped(2),
		mapped(5, 0, 9, 10),
	}}}}
	cases := []struct {
		line, column uint64
		want         LookupResult
		reason       string
	}{
		{4, 5, LookupResult{Mapped: true, SourcePosition: SourcePosition{0, 9, 10}}, "equal generated column hits that segment"},
		{4, 6, LookupResult{Mapped: true, SourcePosition: SourcePosition{0, 9, 11}}, "column offset is added to source column"},
		{4, 2, LookupResult{}, "an unmapped segment is not mapped"},
		{3, 100, LookupResult{}, "lookup never backtracks to an earlier generated row"},
	}
	for _, tc := range cases {
		got, err := mapping.Lookup(tc.line, tc.column)
		logDecision(t, [2]uint64{tc.line, tc.column}, got, tc.want, tc.reason)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("got %#v, want %#v", got, tc.want)
		}
	}
}

func TestCanonicalForm(t *testing.T) {
	input := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 2, Segments: []Segment{
		unmapped(0),
		mapped(1, 0, 0, 10),
		mapped(4, 0, 0, 13),
		unmapped(7),
		unmapped(9),
	}}}}
	got, err := canonicalize(input)
	want := []Row{{GeneratedLine: 2, Segments: []Segment{
		mapped(1, 0, 0, 10),
		unmapped(7),
	}}}
	logDecision(t, input, got.Rows, want, "omit leading unmapped, merge extendable mapped, collapse consecutive unmapped")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Rows, want) {
		t.Fatalf("got %#v, want %#v", got.Rows, want)
	}
}

func TestCanonicalEquivalentMappings(t *testing.T) {
	a := Mapping{SourceCount: 2, Rows: []Row{
		{GeneratedLine: 5, Segments: []Segment{mapped(0, 1, 3, 4), mapped(2, 1, 3, 6)}},
		{GeneratedLine: 1, Segments: []Segment{unmapped(0), unmapped(3)}},
	}}
	b := Mapping{SourceCount: 2, Rows: []Row{
		{GeneratedLine: 1, Segments: []Segment{unmapped(3)}},
		{GeneratedLine: 5, Segments: []Segment{mapped(0, 1, 3, 4)}},
	}}
	canonicalA, err := canonicalize(a)
	if err != nil {
		t.Fatal(err)
	}
	canonicalB, err := canonicalize(b)
	if err != nil {
		t.Fatal(err)
	}
	logDecision(t, [2]Mapping{a, b}, canonicalA, canonicalB, "equivalent mappings normalize independently")
	if !reflect.DeepEqual(canonicalA, canonicalB) {
		t.Fatalf("canonical mappings differ: %#v vs %#v", canonicalA, canonicalB)
	}
}

func ComposeOrDie(a, b Mapping) Mapping {
	result, err := Compose(a, b)
	if err != nil {
		panic(err)
	}
	return result
}

func TestCompositionAssociativity(t *testing.T) {
	m1 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 0),
		mapped(3, 0, 2, 30),
		unmapped(8),
		mapped(10, 0, 4, 100),
		unmapped(15),
	}}}}
	m2 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 1),
		mapped(4, 0, 0, 6),
		unmapped(12),
		mapped(15, 0, 0, 0),
	}}}}
	m3 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 0),
		mapped(2, 0, 0, 2),
		mapped(7, 0, 0, 7),
		unmapped(18),
	}}}}

	left, err := Compose(m3, ComposeOrDie(m2, m1))
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compose(ComposeOrDie(m3, m2), m1)
	if err != nil {
		t.Fatal(err)
	}
	logDecision(t, [3]Mapping{m1, m2, m3}, left, right, "canonicalization makes either parenthesization identical")
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("composition is not associative: %#v vs %#v", left, right)
	}
}

func TestValidationAndOverflowErrors(t *testing.T) {
	invalidM1 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{mapped(0, 1, 0, 0)}}}}
	invalidM2 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{mapped(1, 0, 0, 0), mapped(1, 0, 0, 0)}}}}
	_, err := Compose(invalidM2, invalidM1)
	logDecision(t, [2]Mapping{invalidM2, invalidM1}, CategoryOf(err), CategoryInvalidArgument, "M1 validation is checked before M2 validation")
	if CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("got category %q", CategoryOf(err))
	}

	overflowM2 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, 0)}}}}
	overflowM1 := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, MaxCoordinate)}}}}
	_, err = Compose(overflowM2, overflowM1)
	logDecision(t, [2]Mapping{overflowM2, overflowM1}, CategoryOf(err), CategoryPositionOverflow, "offset 1 drives source column above 10^9")
	if CategoryOf(err) != CategoryPositionOverflow {
		t.Fatalf("got category %q", CategoryOf(err))
	}
}

func TestServiceNamesAndConcurrency(t *testing.T) {
	service := NewService()
	base := Mapping{SourceCount: 1, Rows: []Row{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, 0)}}}}
	if err := service.Register("a", base); err != nil {
		t.Fatal(err)
	}
	if err := service.Register("a", base); CategoryOf(err) != CategoryDuplicateName {
		t.Fatalf("duplicate category = %q", CategoryOf(err))
	}
	if err := service.Compose("missing", "a", "x"); CategoryOf(err) != CategoryNotFound {
		t.Fatalf("not found category = %q", CategoryOf(err))
	}

	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = service.Register("dup", base)
			_, _ = service.Lookup("a", 0, 0)
		}()
	}
	wait.Wait()

	got, ok := service.Get("dup")
	logDecision(t, "32 concurrent registrations of dup", got, base, "exactly one registration is accepted and reads remain stable")
	if !ok || !reflect.DeepEqual(got, base) {
		t.Fatalf("concurrent registration state invalid: ok=%v got=%#v", ok, got)
	}
}
