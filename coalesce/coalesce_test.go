package coalesce

import (
	"errors"
	"reflect"
	"testing"

	"ontology/rangespec"
)

func span(a, b int64) rangespec.Spec { return rangespec.Spec{Kind: rangespec.Span, From: a, To: b} }
func open(a int64) rangespec.Spec    { return rangespec.Spec{Kind: rangespec.Open, From: a} }
func suffix(n int64) rangespec.Spec  { return rangespec.Spec{Kind: rangespec.Suffix, N: n} }

func TestClipOutOfBounds(t *testing.T) {
	// b past the end is clipped to the end, not an error.
	rs, err := Normalize([]rangespec.Spec{span(2, 999)}, 10)
	if err != nil || !reflect.DeepEqual(rs, []Range{{2, 9}}) {
		t.Fatalf("span clip: %v %v", rs, err)
	}
	// -n with n > size takes the whole resource.
	rs, err = Normalize([]rangespec.Spec{suffix(1000)}, 10)
	if err != nil || !reflect.DeepEqual(rs, []Range{{0, 9}}) {
		t.Fatalf("suffix clip: %v %v", rs, err)
	}
	// a- past the end is dropped; being the only range it is unsatisfiable.
	_, err = Normalize([]rangespec.Spec{open(10)}, 10)
	var ue *UnsatisfiableError
	if !errors.As(err, &ue) || ue.Size != 10 {
		t.Fatalf("open past end: %v", err)
	}
}

func TestSuffixZeroUnsatisfiable(t *testing.T) {
	_, err := Normalize([]rangespec.Spec{suffix(0)}, 42)
	var ue *UnsatisfiableError
	if !errors.As(err, &ue) {
		t.Fatalf("bytes=-0 must be unsatisfiable, got %v", err)
	}
	if ue.Size != 42 {
		t.Fatalf("UnsatisfiableError.Size = %d, want 42", ue.Size)
	}
}

func TestMergeOverlapAndAdjacency(t *testing.T) {
	rs, err := Normalize([]rangespec.Spec{
		span(8, 9), span(0, 1), span(2, 3), span(5, 6), span(4, 4),
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 0-1,2-3 adjacent -> 0-3; 4-4,5-6 adjacent -> 4-6; 0-3 and 4-6
	// adjacent -> 0-6; 8-9 stays separate.
	want := []Range{{0, 6}, {8, 9}}
	if !reflect.DeepEqual(rs, want) {
		t.Fatalf("got %v, want %v", rs, want)
	}
}

func TestResultIsSortedAndDisjoint(t *testing.T) {
	specs := []rangespec.Spec{span(50, 60), span(0, 10), span(30, 40), span(20, 25)}
	rs, err := Normalize(specs, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(rs); i++ {
		if rs[i].From <= rs[i-1].To+1 {
			t.Fatalf("ranges %v overlap or are adjacent", rs)
		}
	}
}

func TestInvertedSpanDropped(t *testing.T) {
	// "5-3" has To < From: syntactically valid, covers nothing.
	rs, err := Normalize([]rangespec.Spec{span(5, 3), span(0, 1)}, 10)
	if err != nil || !reflect.DeepEqual(rs, []Range{{0, 1}}) {
		t.Fatalf("got %v %v", rs, err)
	}
}
