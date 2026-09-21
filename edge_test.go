package ontology

import (
	"reflect"
	"testing"
)

func TestSingleStream(t *testing.T) {
	s := []float64{1, 1, 2, 3, 3, 3}
	checkEqual(t, "single union set", mustRun(t, OpUnion, Set, s), []float64{1, 2, 3})
	checkEqual(t, "single union multiset", mustRun(t, OpUnion, Multiset, s), s)
	checkEqual(t, "single intersect set", mustRun(t, OpIntersect, Set, s), []float64{1, 2, 3})
	checkEqual(t, "single intersect multiset", mustRun(t, OpIntersect, Multiset, s), s)
	checkEqual(t, "single difference set", mustRun(t, OpDifference, Set, s), []float64{1, 2, 3})
	checkEqual(t, "single difference multiset", mustRun(t, OpDifference, Multiset, s), s)
}

func TestEmptyStreamAmongOthers(t *testing.T) {
	full := []float64{1, 2}
	empty := []float64{}
	checkEqual(t, "union ignores empty", mustRun(t, OpUnion, Set, full, empty), []float64{1, 2})
	checkEqual(t, "intersect forced empty", mustRun(t, OpIntersect, Set, full, empty), []float64{})
	checkEqual(t, "multiset intersect forced empty",
		mustRun(t, OpIntersect, Multiset, full, empty), []float64{})
	checkEqual(t, "difference subtracts nothing",
		mustRun(t, OpDifference, Multiset, full, empty), []float64{1, 2})
	checkEqual(t, "difference from empty first",
		mustRun(t, OpDifference, Multiset, empty, full), []float64{})
}

func TestAllStreamsEmpty(t *testing.T) {
	a, b := []float64{}, []float64{}
	for _, op := range []Op{OpUnion, OpIntersect, OpDifference} {
		for _, sem := range []Semantics{Set, Multiset} {
			got := mustRun(t, op, sem, a, b)
			if got == nil || len(got) != 0 {
				t.Fatalf("%v/%v of empty streams: want empty non-nil, got %v", op, sem, got)
			}
		}
	}
}

func TestInputsAreNeverModified(t *testing.T) {
	a := []float64{1, 1, 2, 3, 3}
	b := []float64{2, 2, 4}
	snapA := append([]float64(nil), a...)
	snapB := append([]float64(nil), b...)

	results := [][]float64{
		mustRun(t, OpUnion, Set, a, b),
		mustRun(t, OpUnion, Multiset, a, b),
		mustRun(t, OpIntersect, Multiset, a, b),
		mustRun(t, OpDifference, Multiset, a, b),
	}
	if !reflect.DeepEqual(a, snapA) || !reflect.DeepEqual(b, snapB) {
		t.Fatal("inputs were modified")
	}
	// Mutating a result must not leak into the inputs.
	for _, r := range results {
		for i := range r {
			r[i] = -99
		}
	}
	if !reflect.DeepEqual(a, snapA) || !reflect.DeepEqual(b, snapB) {
		t.Fatal("result shares memory with inputs")
	}
	// Results are freshly allocated per call.
	again := mustRun(t, OpUnion, Multiset, a, b)
	checkEqual(t, "repeat call unaffected", again, []float64{1, 1, 2, 2, 3, 3, 4})
}

func TestResultDoesNotAliasInputs(t *testing.T) {
	s := []float64{1, 2, 3}
	got := mustRun(t, OpUnion, Multiset, s)
	got[0] = 42
	if s[0] != 1 {
		t.Fatal("result aliases the input slice")
	}
}
