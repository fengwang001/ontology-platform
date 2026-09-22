package coalesce

import (
	"errors"
	"testing"

	"ontology/rangespec"
)

func TestNormalizeClipsAndConvertsRanges(t *testing.T) {
	headerRanges := []rangespec.Interval{
		{Start: 2, End: 3},
		{Start: 8, End: 100},
		{Start: 6, End: -1},
		{Start: -1, End: 100},
	}
	got, err := Normalize(headerRanges, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Range{{Start: 0, End: 10}}
	assertRanges(t, got, want)
}

func TestNormalizeSuffixZeroUnsatisfiableWithLength(t *testing.T) {
	_, err := Normalize([]rangespec.Interval{{Start: -1, End: 0}}, 10)
	length, ok := IsUnsatisfiable(err)
	if !ok || length != 10 {
		t.Fatalf("err=%v ok=%v length=%d", err, ok, length)
	}
	var unsatisfiable UnsatisfiableError
	if !errors.As(err, &unsatisfiable) || unsatisfiable.ResourceLength != 10 {
		t.Fatalf("typed error = %#v", err)
	}
}

func TestNormalizeStartAtOrPastEOFUnsatisfiable(t *testing.T) {
	cases := []rangespec.Interval{
		{Start: 10, End: 20},
		{Start: 10, End: -1},
	}
	for _, item := range cases {
		_, err := Normalize([]rangespec.Interval{item}, 10)
		if length, ok := IsUnsatisfiable(err); !ok || length != 10 {
			t.Fatalf("item %+v err=%v length=%d", item, err, length)
		}
	}
}

func TestNormalizeMergesOverlappingAndAdjacent(t *testing.T) {
	items := []rangespec.Interval{
		{Start: 8, End: 9},
		{Start: 0, End: 2},
		{Start: 2, End: 4},
		{Start: 4, End: 7},
		{Start: 11, End: 12},
	}
	got, err := Normalize(items, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := []Range{{Start: 0, End: 10}, {Start: 11, End: 13}}
	assertRanges(t, got, want)
}

func TestNormalizeSuffixAndLastByteMergeToOne(t *testing.T) {
	items := []rangespec.Interval{{Start: -1, End: 2}, {Start: 8, End: 9}}
	got, err := Normalize(items, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertRanges(t, got, []Range{{Start: 8, End: 10}})
}

func assertRanges(t *testing.T, got, want []Range) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ranges = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ranges[%d] = %+v, want %+v; all=%v", i, got[i], want[i], got)
		}
	}
}
