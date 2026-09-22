package coalesce

import (
	"errors"
	"testing"

	"ontology/rangespec"
)

func TestResolveFormsAndClipping(t *testing.T) {
	const total = int64(100)
	cases := []struct {
		spec rangespec.RangeSpec
		want Interval
	}{
		{rangespec.RangeSpec{Kind: rangespec.KindClosed, Start: 0, End: 49}, Interval{0, 49}},
		// end past the representation: clip to the end, no error
		{rangespec.RangeSpec{Kind: rangespec.KindClosed, Start: 90, End: 500}, Interval{90, 99}},
		{rangespec.RangeSpec{Kind: rangespec.KindOpenEnd, Start: 80}, Interval{80, 99}},
		{rangespec.RangeSpec{Kind: rangespec.KindOpenEnd, Start: 99}, Interval{99, 99}},
		{rangespec.RangeSpec{Kind: rangespec.KindSuffix, Suffix: 10}, Interval{90, 99}},
		// suffix larger than the representation: take all of it
		{rangespec.RangeSpec{Kind: rangespec.KindSuffix, Suffix: 1000}, Interval{0, 99}},
	}
	for _, c := range cases {
		got, err := Resolve(c.spec, total)
		if err != nil {
			t.Fatalf("%+v: unexpected error %v", c.spec, err)
		}
		if got != c.want {
			t.Errorf("%+v: got %v, want %v", c.spec, got, c.want)
		}
	}
}

func TestResolveUnsatisfiable(t *testing.T) {
	cases := []struct {
		spec  rangespec.RangeSpec
		total int64
	}{
		{rangespec.RangeSpec{Kind: rangespec.KindClosed, Start: 100, End: 200}, 100}, // start at end
		{rangespec.RangeSpec{Kind: rangespec.KindClosed, Start: 50, End: 40}, 100},   // reversed
		{rangespec.RangeSpec{Kind: rangespec.KindOpenEnd, Start: 100}, 100},
		{rangespec.RangeSpec{Kind: rangespec.KindSuffix, Suffix: 10}, 0},
		{rangespec.RangeSpec{Kind: rangespec.KindClosed, Start: 0, End: 0}, 0},
	}
	for _, c := range cases {
		_, err := Resolve(c.spec, c.total)
		var ue *UnsatisfiableError
		if !errors.As(err, &ue) {
			t.Fatalf("%+v total=%d: want unsatisfiable, got %v", c.spec, c.total, err)
		}
		if ue.TotalSize != c.total {
			t.Errorf("reported total %d, want %d", ue.TotalSize, c.total)
		}
	}
}

func TestMinusZeroUnsatisfiableWithTotal(t *testing.T) {
	for _, total := range []int64{0, 1, 100} {
		_, err := Resolve(rangespec.RangeSpec{Kind: rangespec.KindSuffix, Suffix: 0}, total)
		var ue *UnsatisfiableError
		if !errors.As(err, &ue) {
			t.Fatalf("total=%d: -0 must be unsatisfiable, got %v", total, err)
		}
		if ue.TotalSize != total {
			t.Fatalf("total=%d: error reports %d", total, ue.TotalSize)
		}
	}
}

func TestSyntaxVsUnsatisfiableDistinct(t *testing.T) {
	_, se := rangespec.Parse("bytes=garbage")
	_, ue := Resolve(rangespec.RangeSpec{Kind: rangespec.KindSuffix, Suffix: 0}, 10)
	var syntax *rangespec.SyntaxError
	var unsat *UnsatisfiableError
	if !errors.As(se, &syntax) {
		t.Fatal("parse error is not *SyntaxError")
	}
	if errors.As(se, &unsat) {
		t.Fatal("syntax error must not also classify as unsatisfiable")
	}
	if !errors.As(ue, &unsat) {
		t.Fatal("resolve error is not *UnsatisfiableError")
	}
	if errors.As(ue, &syntax) {
		t.Fatal("unsatisfiable error must not also classify as syntax")
	}
}
