package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestNaNErrorCarriesStreamAndIndex(t *testing.T) {
	streams := [][]float64{
		{1, 2, 3},
		{0, math.NaN(), 4},
	}
	for _, op := range []Op{OpUnion, OpIntersect, OpDifference} {
		got, _, err := compute(op, Set, streams)
		if got != nil {
			t.Fatalf("op %v: result must be nil on NaN, got %v", op, got)
		}
		var nanErr *NaNError
		if !errors.As(err, &nanErr) {
			t.Fatalf("op %v: want *NaNError, got %v", op, err)
		}
		if nanErr.Stream != 1 || nanErr.Index != 1 {
			t.Fatalf("op %v: want stream 1 index 1, got stream %d index %d",
				op, nanErr.Stream, nanErr.Index)
		}
	}
}

func TestNaNIsNotSilentlySkipped(t *testing.T) {
	// A NaN hidden behind valid elements must still abort the operation.
	_, _, err := Union(Multiset, []float64{1, 2, math.NaN()})
	var nanErr *NaNError
	if !errors.As(err, &nanErr) || nanErr.Index != 2 {
		t.Fatalf("want NaNError at index 2, got %v", err)
	}
}

func TestOrderErrorCarriesStreamAndIndex(t *testing.T) {
	streams := [][]float64{
		{1, 2, 3},
		{5, 4},
	}
	_, _, err := Intersect(Set, streams...)
	var ordErr *OrderError
	if !errors.As(err, &ordErr) {
		t.Fatalf("want *OrderError, got %v", err)
	}
	if ordErr.Stream != 1 || ordErr.Index != 1 {
		t.Fatalf("want stream 1 index 1, got stream %d index %d",
			ordErr.Stream, ordErr.Index)
	}
}

func TestEqualAdjacentElementsAreNotUnsorted(t *testing.T) {
	got, _, err := Union(Multiset, []float64{1, 1, 1, 2, 2}, []float64{2, 2, 2})
	if err != nil {
		t.Fatalf("equal adjacent elements must be accepted: %v", err)
	}
	checkEqual(t, "duplicates merge", got, []float64{1, 1, 1, 2, 2, 2})
}

func TestZeroStreamIntersectAndDifferenceAreErrors(t *testing.T) {
	got, _, err := Intersect(Set)
	if got != nil || !errors.Is(err, ErrEmptyIntersect) {
		t.Fatalf("zero-stream intersect: got %v, err %v", got, err)
	}
	got, _, err = Intersect(Multiset)
	if got != nil || !errors.Is(err, ErrEmptyIntersect) {
		t.Fatalf("zero-stream multiset intersect: got %v, err %v", got, err)
	}
	got, _, err = Difference(Set)
	if got != nil || !errors.Is(err, ErrEmptyDifference) {
		t.Fatalf("zero-stream difference: got %v, err %v", got, err)
	}
	got, _, err = Difference(Multiset)
	if got != nil || !errors.Is(err, ErrEmptyDifference) {
		t.Fatalf("zero-stream multiset difference: got %v, err %v", got, err)
	}
}

func TestZeroStreamUnionIsEmptyNotError(t *testing.T) {
	got, stats, err := Union(Set)
	if err != nil {
		t.Fatalf("zero-stream union must not error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("zero-stream union must be empty non-nil, got %v", got)
	}
	if stats.Comparisons != 0 || stats.MaxHeapSize != 0 {
		t.Fatalf("zero-stream union must do no work, got %+v", stats)
	}
}
