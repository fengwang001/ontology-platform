package streamset

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func mustCompute(t *testing.T, op Op, sem Semantics, streams ...[]float64) []float64 {
	t.Helper()
	got, _, err := Compute(op, sem, streams...)
	if err != nil {
		t.Fatalf("%s/%s: unexpected error: %v", op, sem, err)
	}
	return got
}

func TestSetVsMultisetUnion(t *testing.T) {
	s1 := []float64{1, 1, 2, 2}
	s2 := []float64{1, 1, 2, 2, 2}
	set := mustCompute(t, OpUnion, Set, s1, s2)
	multi := mustCompute(t, OpUnion, Multiset, s1, s2)
	if want := []float64{1, 2}; !reflect.DeepEqual(set, want) {
		t.Fatalf("set union = %v, want %v", set, want)
	}
	if want := []float64{1, 1, 2, 2, 2}; !reflect.DeepEqual(multi, want) {
		t.Fatalf("multiset union = %v, want %v", multi, want)
	}
}

func TestSetVsMultisetIntersect(t *testing.T) {
	s1 := []float64{1, 1, 2, 2}
	s2 := []float64{1, 1, 2, 2, 2}
	set := mustCompute(t, OpIntersect, Set, s1, s2)
	multi := mustCompute(t, OpIntersect, Multiset, s1, s2)
	if want := []float64{1, 2}; !reflect.DeepEqual(set, want) {
		t.Fatalf("set intersect = %v, want %v", set, want)
	}
	if want := []float64{1, 1, 2, 2}; !reflect.DeepEqual(multi, want) {
		t.Fatalf("multiset intersect = %v, want %v", multi, want)
	}
}

func TestMultisetUnionTakesMaxIntersectTakesMin(t *testing.T) {
	s1 := []float64{5, 5, 5}
	s2 := []float64{5}
	s3 := []float64{5, 5}
	if got, want := mustCompute(t, OpUnion, Multiset, s1, s2, s3), []float64{5, 5, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("union max count = %v, want %v", got, want)
	}
	if got, want := mustCompute(t, OpIntersect, Multiset, s1, s2, s3), []float64{5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("intersect min count = %v, want %v", got, want)
	}
}

func TestDifference(t *testing.T) {
	s1 := []float64{1, 1, 1, 2, 3}
	s2 := []float64{1}
	s3 := []float64{3, 4}
	multi := mustCompute(t, OpDifference, Multiset, s1, s2, s3)
	if want := []float64{1, 1, 2}; !reflect.DeepEqual(multi, want) {
		t.Fatalf("multiset difference = %v, want %v", multi, want)
	}
	set := mustCompute(t, OpDifference, Set, s1, s2, s3)
	if want := []float64{2}; !reflect.DeepEqual(set, want) {
		t.Fatalf("set difference = %v, want %v", set, want)
	}
}

func TestDifferenceNeverNegative(t *testing.T) {
	got := mustCompute(t, OpDifference, Multiset, []float64{1}, []float64{1, 1, 1})
	if len(got) != 0 {
		t.Fatalf("difference floored at zero, got %v", got)
	}
	got = mustCompute(t, OpDifference, Multiset, []float64{2, 2}, []float64{2, 2, 2, 2}, []float64{2})
	if len(got) != 0 {
		t.Fatalf("difference floored at zero, got %v", got)
	}
}

func TestSignedZeroSameValue(t *testing.T) {
	pos := []float64{0.0}
	neg := []float64{math.Copysign(0, -1)}
	if got := mustCompute(t, OpUnion, Set, pos, neg); len(got) != 1 || got[0] != 0 {
		t.Fatalf("set union of +0/-0 = %v, want one zero", got)
	}
	if got := mustCompute(t, OpIntersect, Multiset, pos, neg); len(got) != 1 {
		t.Fatalf("multiset intersect of +0/-0 = %v, want one zero", got)
	}
	if got := mustCompute(t, OpDifference, Set, pos, neg); len(got) != 0 {
		t.Fatalf("set difference of +0/-0 = %v, want empty", got)
	}
}

func TestInfinitiesAreLegal(t *testing.T) {
	s1 := []float64{math.Inf(-1), 0, math.Inf(1)}
	s2 := []float64{math.Inf(-1), math.Inf(1)}
	if got, want := mustCompute(t, OpIntersect, Set, s1, s2), []float64{math.Inf(-1), math.Inf(1)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("intersect with infinities = %v, want %v", got, want)
	}
}

func TestEdgeCases(t *testing.T) {
	if got := mustCompute(t, OpUnion, Set); len(got) != 0 {
		t.Fatalf("union of zero streams = %v, want empty", got)
	}
	if got := mustCompute(t, OpDifference, Multiset); len(got) != 0 {
		t.Fatalf("difference of zero streams = %v, want empty", got)
	}
	if _, _, err := Intersect(Set); !errors.Is(err, ErrEmptyIntersection) {
		t.Fatalf("intersect of zero streams err = %v, want ErrEmptyIntersection", err)
	}
	one := []float64{1, 1, 2}
	if got, want := mustCompute(t, OpUnion, Set, one), []float64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("one-stream set union = %v, want %v", got, want)
	}
	if got, want := mustCompute(t, OpIntersect, Multiset, one), []float64{1, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("one-stream multiset intersect = %v, want %v", got, want)
	}
	if got, want := mustCompute(t, OpDifference, Multiset, one), []float64{1, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("one-stream multiset difference = %v, want %v", got, want)
	}
	empty := []float64{}
	if got := mustCompute(t, OpUnion, Set, empty, []float64{7}); !reflect.DeepEqual(got, []float64{7}) {
		t.Fatalf("union with empty stream = %v, want [7]", got)
	}
	if got := mustCompute(t, OpIntersect, Set, empty, []float64{7}); len(got) != 0 {
		t.Fatalf("intersect with empty stream = %v, want empty", got)
	}
	if got := mustCompute(t, OpUnion, Multiset, empty, empty); len(got) != 0 {
		t.Fatalf("union of all-empty streams = %v, want empty", got)
	}
}

func TestInputsNotModified(t *testing.T) {
	s1 := []float64{3, 3, 4}
	s2 := []float64{1, 2}
	c1, c2 := append([]float64(nil), s1...), append([]float64(nil), s2...)
	for _, op := range []Op{OpUnion, OpIntersect, OpDifference} {
		for _, sem := range []Semantics{Set, Multiset} {
			mustCompute(t, op, sem, s1, s2)
		}
	}
	if !reflect.DeepEqual(s1, c1) || !reflect.DeepEqual(s2, c2) {
		t.Fatalf("inputs modified: %v %v", s1, s2)
	}
	out := mustCompute(t, OpUnion, Multiset, s1, s2)
	out[0] = 99
	if s1[0] != 3 || s2[0] != 1 {
		t.Fatalf("result aliases input")
	}
}
