package ontology

import (
	"math"
	"reflect"
	"testing"
)

// dupStreams holds repeated elements so set and multiset semantics
// produce visibly different results.
var dupStreams = [][]float64{
	{1, 1, 2, 2, 2, 3},
	{1, 2, 2, 4},
}

func mustRun(t *testing.T, op Op, sem Semantics, streams ...[]float64) []float64 {
	t.Helper()
	var got []float64
	var err error
	switch op {
	case OpUnion:
		got, _, err = Union(sem, streams...)
	case OpIntersect:
		got, _, err = Intersect(sem, streams...)
	default:
		got, _, err = Difference(sem, streams...)
	}
	if err != nil {
		t.Fatalf("%v/%v: unexpected error: %v", op, sem, err)
	}
	return got
}

func checkEqual(t *testing.T, what string, got, want []float64) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func TestUnionSetVsMultiset(t *testing.T) {
	set := mustRun(t, OpUnion, Set, dupStreams...)
	multi := mustRun(t, OpUnion, Multiset, dupStreams...)
	checkEqual(t, "set union", set, []float64{1, 2, 3, 4})
	// Multiset union keeps per-stream maxima: 1 occurs twice in stream 0.
	checkEqual(t, "multiset union", multi, []float64{1, 1, 2, 2, 2, 3, 4})
	if reflect.DeepEqual(set, multi) {
		t.Fatal("set and multiset union must differ on duplicate input")
	}
}

func TestIntersectSetVsMultiset(t *testing.T) {
	set := mustRun(t, OpIntersect, Set, dupStreams...)
	multi := mustRun(t, OpIntersect, Multiset, dupStreams...)
	checkEqual(t, "set intersect", set, []float64{1, 2})
	// Multiset intersect keeps the per-stream minimum multiplicity.
	checkEqual(t, "multiset intersect", multi, []float64{1, 2, 2})
	if reflect.DeepEqual(set, multi) {
		t.Fatal("set and multiset intersect must differ on duplicate input")
	}
}

func TestUnionTakesMaxIntersectTakesMin(t *testing.T) {
	streams := [][]float64{
		{5, 5, 5},
		{5},
		{5, 5},
	}
	checkEqual(t, "union=max", mustRun(t, OpUnion, Multiset, streams...), []float64{5, 5, 5})
	checkEqual(t, "intersect=min", mustRun(t, OpIntersect, Multiset, streams...), []float64{5})
}

func TestDifferenceMultisetFloorAtZero(t *testing.T) {
	minuend := []float64{1, 1, 2, 3}
	subtrahend := []float64{1, 1, 1, 2, 2}
	got := mustRun(t, OpDifference, Multiset, minuend, subtrahend)
	// 1: 2-3 -> 0 (floored), 2: 1-2 -> 0 (floored), 3: 1-0 -> 1.
	checkEqual(t, "multiset difference", got, []float64{3})
}

func TestDifferenceSetSemantics(t *testing.T) {
	got := mustRun(t, OpDifference, Set, []float64{1, 1, 2, 3, 3}, []float64{2}, []float64{4})
	checkEqual(t, "set difference", got, []float64{1, 3})
}

func TestSignedZerosAreSameValue(t *testing.T) {
	negZero := math.Copysign(0, -1)
	streams := [][]float64{{negZero}, {0.0}}

	union := mustRun(t, OpUnion, Set, streams...)
	checkEqual(t, "union of -0 and +0", union, []float64{0})
	if math.Signbit(union[0]) {
		t.Fatal("result must carry +0.0, not -0.0")
	}

	inter := mustRun(t, OpIntersect, Set, streams...)
	checkEqual(t, "intersect of -0 and +0", inter, []float64{0})

	multi := mustRun(t, OpUnion, Multiset, []float64{negZero, negZero}, []float64{0.0})
	checkEqual(t, "multiset union of zeros", multi, []float64{0, 0})
}

func TestInfinitiesAreOrdinaryElements(t *testing.T) {
	a := []float64{math.Inf(-1), 0, math.Inf(1)}
	b := []float64{math.Inf(-1), math.Inf(1)}
	checkEqual(t, "intersect with infinities",
		mustRun(t, OpIntersect, Set, a, b),
		[]float64{math.Inf(-1), math.Inf(1)})
	checkEqual(t, "difference drops infinities",
		mustRun(t, OpDifference, Set, a, b),
		[]float64{0})
}

func TestResultSortedAndDistinctUnderSet(t *testing.T) {
	got := mustRun(t, OpUnion, Set, []float64{1, 1, 3}, []float64{2, 3, 3}, []float64{0})
	checkEqual(t, "set union sorted", got, []float64{0, 1, 2, 3})
}
