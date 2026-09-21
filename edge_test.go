package ontology

import (
	"errors"
	"slices"
	"testing"
)

// 零条流：并集为空且无错误；交集与差集返回 ErrNoStreams。
func TestZeroStreams(t *testing.T) {
	got, _, err := Union(Set)
	if err != nil || len(got) != 0 {
		t.Fatalf("union of zero streams: got %v, err %v", got, err)
	}
	got, _, err = Union(Multiset)
	if err != nil || len(got) != 0 {
		t.Fatalf("multiset union of zero streams: got %v, err %v", got, err)
	}
	if _, _, err = Intersect(Set); !errors.Is(err, ErrNoStreams) {
		t.Fatalf("intersect of zero streams: got %v, want ErrNoStreams", err)
	}
	if _, _, err = Intersect(Multiset); !errors.Is(err, ErrNoStreams) {
		t.Fatalf("multiset intersect of zero streams: got %v, want ErrNoStreams", err)
	}
	if _, _, err = Difference(Set); !errors.Is(err, ErrNoStreams) {
		t.Fatalf("difference of zero streams: got %v, want ErrNoStreams", err)
	}
	if _, _, err = Difference(Multiset); !errors.Is(err, ErrNoStreams) {
		t.Fatalf("multiset difference of zero streams: got %v, want ErrNoStreams", err)
	}
}

// 一条流：并/交/差都是它本身（集合语义下去重）。
func TestSingleStream(t *testing.T) {
	s := []float64{1, 1, 2, 3, 3}
	got, _, err := Union(Set, s)
	requireResult(t, got, err, []float64{1, 2, 3})
	got, _, err = Union(Multiset, s)
	requireResult(t, got, err, []float64{1, 1, 2, 3, 3})
	got, _, err = Intersect(Set, s)
	requireResult(t, got, err, []float64{1, 2, 3})
	got, _, err = Intersect(Multiset, s)
	requireResult(t, got, err, []float64{1, 1, 2, 3, 3})
	got, _, err = Difference(Set, s)
	requireResult(t, got, err, []float64{1, 2, 3})
	got, _, err = Difference(Multiset, s)
	requireResult(t, got, err, []float64{1, 1, 2, 3, 3})
}

// 某条流为空：并集忽略它；交集为空；差集看第一条流。
func TestSomeEmptyStreams(t *testing.T) {
	got, _, err := Union(Set, []float64{}, []float64{1, 2}, []float64{})
	requireResult(t, got, err, []float64{1, 2})

	got, _, err = Intersect(Set, []float64{}, []float64{1, 2})
	requireResult(t, got, err, nil)

	got, _, err = Difference(Set, []float64{1, 2}, []float64{})
	requireResult(t, got, err, []float64{1, 2})

	got, _, err = Difference(Set, []float64{}, []float64{1, 2})
	requireResult(t, got, err, nil)
}

// 所有流都为空：并集为空，交集为空，差集为空，均无错误。
func TestAllStreamsEmpty(t *testing.T) {
	got, _, err := Union(Set, []float64{}, []float64{})
	requireResult(t, got, err, nil)
	got, _, err = Intersect(Multiset, []float64{}, []float64{})
	requireResult(t, got, err, nil)
	got, _, err = Difference(Set, []float64{}, []float64{})
	requireResult(t, got, err, nil)
}

// 输入切片不得被修改；结果切片与输入互不影响。
func TestInputsAreNotMutated(t *testing.T) {
	s1 := []float64{3, 1, 2} // 故意乱序：也不得被就地排序
	s2 := []float64{1, 2}
	_, _, _ = Union(Set, s1, s2)
	if !slices.Equal(s1, []float64{3, 1, 2}) {
		t.Fatalf("input was modified: %v", s1)
	}

	a := []float64{1, 2, 2}
	b := []float64{2, 3}
	aCopy := slices.Clone(a)
	bCopy := slices.Clone(b)
	got, _, err := Union(Multiset, a, b)
	requireResult(t, got, err, []float64{1, 2, 2, 3})
	if !slices.Equal(a, aCopy) || !slices.Equal(b, bCopy) {
		t.Fatalf("inputs were modified: %v %v", a, b)
	}

	// 修改结果不得影响输入。
	for i := range got {
		got[i] = -1
	}
	if !slices.Equal(a, aCopy) || !slices.Equal(b, bCopy) {
		t.Fatalf("result aliases input: %v %v", a, b)
	}
}
