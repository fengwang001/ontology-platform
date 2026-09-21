package ontology

import (
	"errors"
	"math"
	"testing"
)

// NaN 必须返回可判定的错误，并指出流号与下标。
func TestNaNErrorCarriesLocation(t *testing.T) {
	streams := [][]float64{
		{1, 2},
		{3, math.NaN()},
		{4},
	}
	for _, op := range []func(Semantics, ...[]float64) ([]float64, Stats, error){
		Union, Intersect, Difference,
	} {
		_, _, err := op(Set, streams...)
		var nanErr NaNError
		if !errors.As(err, &nanErr) {
			t.Fatalf("expected NaNError, got %v", err)
		}
		if nanErr.Stream != 1 || nanErr.Index != 1 {
			t.Fatalf("got stream=%d index=%d, want stream=1 index=1",
				nanErr.Stream, nanErr.Index)
		}
	}
}

// NaN 不得静默跳过，也不得污染结果。
func TestNaNDoesNotPolluteResult(t *testing.T) {
	got, _, err := Union(Set, []float64{1}, []float64{math.NaN()})
	if err == nil {
		t.Fatalf("expected error, got result %v", got)
	}
	if got != nil {
		t.Fatalf("result must be nil on error, got %v", got)
	}
}

// 乱序输入必须返回可判定的错误，并指出流号与下标。
func TestOrderErrorCarriesLocation(t *testing.T) {
	streams := [][]float64{
		{1, 2, 3},
		{0, 9, 4},
	}
	_, _, err := Union(Set, streams...)
	var ordErr OrderError
	if !errors.As(err, &ordErr) {
		t.Fatalf("expected OrderError, got %v", err)
	}
	if ordErr.Stream != 1 || ordErr.Index != 2 {
		t.Fatalf("got stream=%d index=%d, want stream=1 index=2",
			ordErr.Stream, ordErr.Index)
	}
	if ordErr.Prev != 9 || ordErr.Cur != 4 {
		t.Fatalf("got prev=%v cur=%v, want prev=9 cur=4", ordErr.Prev, ordErr.Cur)
	}
}

// 相等的相邻元素是允许的，只有严格递减才算违规。
func TestEqualAdjacentElementsAreSorted(t *testing.T) {
	got, _, err := Union(Multiset, []float64{1, 2, 2, 2, 3}, []float64{2, 2})
	requireResult(t, got, err, []float64{1, 2, 2, 2, 3})
}

// 尾部的乱序也必须被发现（每个元素都会被归并到）。
func TestOrderErrorAtTail(t *testing.T) {
	_, _, err := Intersect(Set, []float64{1, 2, 3, 2})
	var ordErr OrderError
	if !errors.As(err, &ordErr) {
		t.Fatalf("expected OrderError, got %v", err)
	}
	if ordErr.Stream != 0 || ordErr.Index != 3 {
		t.Fatalf("got stream=%d index=%d, want stream=0 index=3",
			ordErr.Stream, ordErr.Index)
	}
}
