package geom

import (
	"math"
	"testing"
)

func TestContainsHalfOpen(t *testing.T) {
	r := Rect{X0: 0, Y0: 0, X1: 10, Y1: 10}
	cases := []struct {
		name string
		p    Point
		want bool
	}{
		{"inside", Point{X: 5, Y: 5}, true},
		{"left edge", Point{X: 0, Y: 5}, true},
		{"bottom edge", Point{X: 5, Y: 0}, true},
		{"right edge excluded", Point{X: 10, Y: 5}, false},
		{"top edge excluded", Point{X: 5, Y: 10}, false},
		{"lower-left corner in", Point{X: 0, Y: 0}, true},
		{"lower-right corner out", Point{X: 10, Y: 0}, false},
		{"upper-left corner out", Point{X: 0, Y: 10}, false},
		{"upper-right corner out", Point{X: 10, Y: 10}, false},
		{"signed zero equals zero", Point{X: math.Copysign(0, -1), Y: 0}, true},
		{"nan x", Point{X: math.NaN(), Y: 5}, false},
		{"nan y", Point{X: 5, Y: math.NaN()}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Contains(tc.p); got != tc.want {
				t.Fatalf("Contains(%v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

func TestRectRelations(t *testing.T) {
	r := Rect{0, 0, 10, 10}
	cases := []struct {
		name          string
		s             Rect
		wantDisjoint  bool
		wantContained bool
	}{
		{"overlap corner", Rect{5, 5, 15, 15}, false, false},
		{"share edge half-open disjoint", Rect{10, 0, 20, 10}, true, false},
		{"fully inside", Rect{2, 2, 8, 8}, false, true},
		{"equal", r, false, true},
		{"far away", Rect{20, 20, 30, 30}, true, false},
		{"degenerate on upper edge whole-take is harmless", Rect{10, 10, 10, 10}, true, true},
		{"point inside contained", Rect{5, 5, 5, 5}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Disjoint(tc.s); got != tc.wantDisjoint {
				t.Fatalf("Disjoint = %v, want %v", got, tc.wantDisjoint)
			}
			if got := r.ContainsRect(tc.s); got != tc.wantContained {
				t.Fatalf("ContainsRect = %v, want %v", got, tc.wantContained)
			}
		})
	}
}

func TestFinite(t *testing.T) {
	cases := []struct {
		name string
		v    float64
		want bool
	}{
		{"zero", 0, true}, {"negative zero", math.Copysign(0, -1), true},
		{"max", math.MaxFloat64, true}, {"smallest", -math.MaxFloat64, true},
		{"+inf", math.Inf(1), false}, {"-inf", math.Inf(-1), false},
		{"nan", math.NaN(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if Finite(tc.v) != tc.want {
				t.Fatalf("Finite(%v) mismatch", tc.v)
			}
		})
	}
}
