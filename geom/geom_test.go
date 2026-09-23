package geom

import (
	"math"
	"testing"
)

func TestContains(t *testing.T) {
	r := Rect{0, 0, 10, 10}
	cases := []struct {
		name string
		p    Point
		want bool
	}{
		{"inside", Point{1, 5, 5}, true},
		{"left-edge", Point{2, 0, 5}, true},
		{"bottom-edge", Point{3, 5, 0}, true},
		{"right-edge-excluded", Point{4, 10, 5}, false},
		{"top-edge-excluded", Point{5, 5, 10}, false},
		{"corner-min", Point{6, 0, 0}, true},
		{"corner-max-excluded", Point{7, 10, 10}, false},
		{"outside", Point{8, -1, 11}, false},
		{"neg-zero-equals-zero", Point{9, math.Copysign(0, -1), math.Copysign(0, -1)}, true},
	}
	for _, c := range cases {
		if got := r.Contains(c.p); got != c.want {
			t.Errorf("%s: Contains(%v) = %v, want %v", c.name, c.p, got, c.want)
		}
	}
}

func TestIntersects(t *testing.T) {
	r := Rect{0, 0, 10, 10}
	cases := []struct {
		name string
		s    Rect
		want bool
	}{
		{"overlap", Rect{5, 5, 15, 15}, true},
		{"contained", Rect{2, 2, 8, 8}, true},
		{"touch-right-open", Rect{10, 0, 20, 10}, false},
		{"touch-left-open", Rect{-10, 0, 0, 10}, false},
		{"disjoint", Rect{20, 20, 30, 30}, false},
		{"degenerate-line", Rect{5, 5, 5, 20}, false},
		{"degenerate-point", Rect{5, 5, 5, 5}, false},
	}
	for _, c := range cases {
		if got := r.Intersects(c.s); got != c.want {
			t.Errorf("%s: Intersects(%v) = %v, want %v", c.name, c.s, got, c.want)
		}
	}
}

func TestContainsRectAndEmpty(t *testing.T) {
	r := Rect{0, 0, 10, 10}
	cases := []struct {
		name    string
		s       Rect
		contain bool
		empty   bool
	}{
		{"inner", Rect{2, 2, 8, 8}, true, false},
		{"same", r, true, false},
		{"flush-left-open", Rect{0, 0, 10, 10}, true, false},
		{"protrudes-right", Rect{5, 5, 11, 8}, false, false},
		{"zero-width", Rect{5, 5, 5, 8}, true, true},
		{"zero-height", Rect{5, 5, 8, 5}, true, true},
		{"point", Rect{5, 5, 5, 5}, true, true},
	}
	for _, c := range cases {
		if got := r.ContainsRect(c.s); got != c.contain {
			t.Errorf("%s: ContainsRect = %v, want %v", c.name, got, c.contain)
		}
		if got := c.s.Empty(); got != c.empty {
			t.Errorf("%s: Empty = %v, want %v", c.name, got, c.empty)
		}
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		name string
		p    Point
		want bool
	}{
		{"normal", Point{1, 1, 1}, true},
		{"zero", Point{2, 0, 0}, true},
		{"neg-zero", Point{3, -0, -0}, true},
		{"nan-x", Point{4, math.NaN(), 1}, false},
		{"nan-y", Point{5, 1, math.NaN()}, false},
		{"inf-x", Point{6, math.Inf(1), 1}, false},
		{"neg-inf-y", Point{7, 1, math.Inf(-1)}, false},
	}
	for _, c := range cases {
		if got := ValidPoint(c.p); got != c.want {
			t.Errorf("%s: ValidPoint = %v, want %v", c.name, got, c.want)
		}
	}
}
