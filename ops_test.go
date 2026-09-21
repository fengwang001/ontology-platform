package ontology

import (
	"math"
	"slices"
	"testing"
)

func requireResult(t *testing.T, got []float64, err error, want []float64) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// 同一批含重复元素的输入，两种语义的结果必须明显不同。
func TestSetVsMultisetUnion(t *testing.T) {
	s1 := []float64{1, 2, 2, 3, 3, 3}
	s2 := []float64{2, 2, 3, 4}
	s3 := []float64{3, 3, 5}

	set, _, err := Union(Set, s1, s2, s3)
	requireResult(t, set, err, []float64{1, 2, 3, 4, 5})

	multi, _, err := Union(Multiset, s1, s2, s3)
	requireResult(t, multi, err, []float64{1, 2, 2, 3, 3, 3, 4, 5})

	if slices.Equal(set, multi) {
		t.Fatal("set and multiset union must differ on duplicated input")
	}
}

func TestSetVsMultisetIntersect(t *testing.T) {
	s1 := []float64{2, 2, 2}
	s2 := []float64{2, 2}
	s3 := []float64{2, 2, 2, 2}

	set, _, err := Intersect(Set, s1, s2, s3)
	requireResult(t, set, err, []float64{2})

	multi, _, err := Intersect(Multiset, s1, s2, s3)
	requireResult(t, multi, err, []float64{2, 2})

	if slices.Equal(set, multi) {
		t.Fatal("set and multiset intersect must differ on duplicated input")
	}
}

// 多重集并集取各流次数的最大值。
func TestMultisetUnionTakesMaxCount(t *testing.T) {
	got, _, err := Union(Multiset,
		[]float64{7, 7, 7},
		[]float64{7},
		[]float64{7, 7},
	)
	requireResult(t, got, err, []float64{7, 7, 7})
}

// 多重集交集取各流次数的最小值。
func TestMultisetIntersectTakesMinCount(t *testing.T) {
	got, _, err := Intersect(Multiset,
		[]float64{7, 7, 7},
		[]float64{7},
		[]float64{7, 7},
	)
	requireResult(t, got, err, []float64{7})
}

// 多重集差集：第一条流的次数减去其余流的次数，不小于零。
func TestMultisetDifferenceNeverNegative(t *testing.T) {
	got, _, err := Difference(Multiset,
		[]float64{1, 2},
		[]float64{1, 1, 1, 2, 2, 2},
	)
	requireResult(t, got, err, nil)

	got, _, err = Difference(Multiset,
		[]float64{5, 5, 5, 5},
		[]float64{5},
		[]float64{5, 5},
	)
	requireResult(t, got, err, []float64{5})
}

// 集合差集：只在第一条流中出现的值。
func TestSetDifference(t *testing.T) {
	got, _, err := Difference(Set,
		[]float64{1, 2, 2, 3, 4},
		[]float64{2, 4},
		[]float64{3},
	)
	requireResult(t, got, err, []float64{1})
}

// +0.0 与 -0.0 视为同一个值。
func TestSignedZeroAreEqual(t *testing.T) {
	negZero := math.Copysign(0, -1)
	posZero := 0.0

	got, _, err := Intersect(Set, []float64{negZero}, []float64{posZero})
	requireResult(t, got, err, []float64{0})

	got, _, err = Union(Set, []float64{negZero, posZero}, []float64{posZero})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("union of signed zeros should have 1 element, got %v", got)
	}

	got, _, err = Difference(Multiset, []float64{negZero}, []float64{posZero})
	requireResult(t, got, err, nil)
}

// 正负无穷是合法元素。
func TestInfinitiesAreValidElements(t *testing.T) {
	inf := math.Inf(1)
	ninf := math.Inf(-1)
	got, _, err := Union(Set, []float64{ninf, 1, inf}, []float64{ninf, inf})
	requireResult(t, got, err, []float64{ninf, 1, inf})

	got, _, err = Intersect(Set, []float64{ninf, 1, inf}, []float64{ninf, inf})
	requireResult(t, got, err, []float64{ninf, inf})
}
