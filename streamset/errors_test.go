package streamset

import (
	"errors"
	"math"
	"testing"
)

func TestNaNErrorLocatesStreamAndIndex(t *testing.T) {
	streams := [][]float64{
		{1, 2},
		{0, math.NaN(), 3},
	}
	for _, op := range []Op{OpUnion, OpIntersect, OpDifference} {
		_, _, err := Compute(op, Set, streams...)
		var nanErr *NaNError
		if !errors.As(err, &nanErr) {
			t.Fatalf("%s: err = %v, want *NaNError", op, err)
		}
		if nanErr.Stream != 1 || nanErr.Index != 1 {
			t.Fatalf("%s: NaNError = %+v, want stream 1 index 1", op, nanErr)
		}
	}
}

func TestNaNNotEqualToItself(t *testing.T) {
	_, _, err := Union(Set, []float64{math.NaN()}, []float64{math.NaN()})
	var nanErr *NaNError
	if !errors.As(err, &nanErr) {
		t.Fatalf("err = %v, want *NaNError (NaN must not merge with NaN)", err)
	}
	if nanErr.Stream != 0 || nanErr.Index != 0 {
		t.Fatalf("NaNError = %+v, want stream 0 index 0", nanErr)
	}
}

func TestOrderErrorLocatesStreamAndIndex(t *testing.T) {
	streams := [][]float64{
		{1, 2, 3},
		{0, 9, 4},
	}
	_, _, err := Union(Multiset, streams...)
	var ordErr *OrderError
	if !errors.As(err, &ordErr) {
		t.Fatalf("err = %v, want *OrderError", err)
	}
	if ordErr.Stream != 1 || ordErr.Index != 2 {
		t.Fatalf("OrderError = %+v, want stream 1 index 2", ordErr)
	}
}

func TestEqualAdjacentElementsAreNotOutOfOrder(t *testing.T) {
	got, _, err := Union(Multiset, []float64{1, 1, 1, 2, 2}, []float64{2, 2, 2})
	if err != nil {
		t.Fatalf("equal duplicates rejected: %v", err)
	}
	if want := []float64{1, 1, 1, 2, 2, 2}; !equalFloats(got, want) {
		t.Fatalf("union = %v, want %v", got, want)
	}
}

func TestErrorMessages(t *testing.T) {
	nanErr := &NaNError{Stream: 2, Index: 5}
	ordErr := &OrderError{Stream: 1, Index: 3}
	if nanErr.Error() == "" || ordErr.Error() == "" {
		t.Fatal("error messages must be non-empty")
	}
	if !errors.Is(ErrEmptyIntersection, ErrEmptyIntersection) {
		t.Fatal("ErrEmptyIntersection must match itself")
	}
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
