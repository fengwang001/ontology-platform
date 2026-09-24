package ivl

import (
	"math"
	"math/bits"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   []Interval
		want string
	}{
		{"empty", nil, ""},
		{"single point", []Interval{{Lo: 7, Hi: 7}}, "7"},
		{"kept apart", []Interval{{Lo: 1, Hi: 3}, {Lo: 6, Hi: 8}}, "1-3:6-8"},
		{"adjacent merge", []Interval{{Lo: 1, Hi: 5}, {Lo: 6, Hi: 9}}, "1-9"},
		{"overlap merge", []Interval{{Lo: 1, Hi: 10}, {Lo: 4, Hi: 6}}, "1-10"},
		{"shuffled input", []Interval{{Lo: 9, Hi: 9}, {Lo: 1, Hi: 5}, {Lo: 7, Hi: 7}, {Lo: 6, Hi: 6}}, "1-7:9"},
		{"maxint64 no false merge", []Interval{{Lo: 1, Hi: 4}, {Lo: math.MaxInt64, Hi: math.MaxInt64}}, "1-4:9223372036854775807"},
		{"touching maxint64 merges", []Interval{{Lo: 1, Hi: math.MaxInt64 - 1}, {Lo: math.MaxInt64, Hi: math.MaxInt64}}, "1-9223372036854775807"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NewSet(c.in).Format(); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestUnionFormat(t *testing.T) {
	cases := []struct {
		a, b []Interval
		want string
	}{
		{[]Interval{{Lo: 1, Hi: 4}}, []Interval{{Lo: 5, Hi: 8}}, "1-8"},
		{[]Interval{{Lo: 1, Hi: 10}}, []Interval{{Lo: 20, Hi: 20}, {Lo: 22, Hi: 22}}, "1-10:20:22"},
	}
	for i, c := range cases {
		if got := NewSet(c.a).Union(NewSet(c.b)).Format(); got != c.want {
			t.Fatalf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestSubtract(t *testing.T) {
	cases := []struct {
		exec []Interval
		src  []Interval
		want string
	}{
		{nil, []Interval{{Lo: 1, Hi: 3}}, "1-3"},
		{[]Interval{{Lo: 3, Hi: 5}, {Lo: 9, Hi: 9}}, []Interval{{Lo: 1, Hi: 10}}, "1-2:6-8:10"},
		{[]Interval{{Lo: 2, Hi: 2}}, []Interval{{Lo: 1, Hi: 3}}, "1:3"},
		{[]Interval{{Lo: 1, Hi: 10}}, []Interval{{Lo: 4, Hi: 6}}, ""},
		{[]Interval{{Lo: 1, Hi: 3}}, []Interval{{Lo: 3, Hi: 5}}, "4-5"},
		{[]Interval{{Lo: math.MaxInt64, Hi: math.MaxInt64}}, []Interval{{Lo: math.MaxInt64 - 1, Hi: math.MaxInt64}}, "9223372036854775806"},
	}
	for i, c := range cases {
		e := NewSet(c.exec)
		if got := (&e).Subtract(NewSet(c.src)).Format(); got != c.want {
			t.Fatalf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

// TestSubtractProbeBound proves ordered location rather than a table
// scan: m non-adjacent executed points, a source interval intersecting
// only one of them must read O(log m)+intersections, not O(m) intervals.
func TestSubtractProbeBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		pts := make([]Interval, m)
		for i := range pts {
			pts[i] = Interval{Lo: int64(2 * (i + 1)), Hi: int64(2 * (i + 1))}
		}
		e := NewSet(pts) // normalized: {2},{4},...,{2m}
		got := (&e).Subtract(NewSet([]Interval{{Lo: 1, Hi: 3}}))
		if got.Format() != "1:3" {
			t.Fatalf("m=%d diff got %q want 1:3", m, got.Format())
		}
		const intersects = 1 // only {2} meets [1,3]
		bound := 2*bits.Len(uint(m)) + 4 + intersects
		if e.probes > bound {
			t.Fatalf("m=%d probes=%d exceed bound %d: looks like a table scan", m, e.probes, bound)
		}
		if e.probes >= m {
			t.Fatalf("m=%d probes=%d grew linearly with m", m, e.probes)
		}
	}
}
