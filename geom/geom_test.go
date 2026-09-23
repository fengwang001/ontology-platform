package geom

import (
	"math"
	"testing"
)

func TestContainsAndIntersect(t *testing.T) {
	r := Rect{0, 0, 10, 10}
	cases := []struct {
		name string
		p    Point
		want bool
	}{
		{"inside", Point{5, 5, 0}, true},
		{"origin corner", Point{0, 0, 0}, true},
		{"left edge", Point{0, 5, 0}, true},
		{"bottom edge", Point{5, 0, 0}, true},
		{"right edge excluded", Point{10, 5, 0}, false},
		{"top edge excluded", Point{5, 10, 0}, false},
		{"top-right corner excluded", Point{10, 10, 0}, false},
		{"split-line x", Point{5, 5, 0}, true},
		{"neg zero equals zero", Point{math.Copysign(0, -1), 0, 0}, true},
		{"outside", Point{-1, 5, 0}, false},
		{"nan", Point{math.NaN(), 5, 0}, false},
	}
	for _, c := range cases {
		if got := r.Contains(c.p); got != c.want {
			t.Errorf("%s: Contains = %v, want %v", c.name, got, c.want)
		}
	}

	ir := []struct {
		name string
		a, b Rect
		want bool
	}{
		{"overlap", Rect{0, 0, 10, 10}, Rect{5, 5, 15, 15}, true},
		{"touch x edge", Rect{0, 0, 10, 10}, Rect{10, 0, 20, 10}, false},
		{"touch corner", Rect{0, 0, 10, 10}, Rect{10, 10, 20, 20}, false},
		{"disjoint", Rect{0, 0, 1, 1}, Rect{5, 5, 6, 6}, false},
		{"degenerate line", Rect{0, 0, 10, 0}, Rect{0, 0, 10, 10}, false},
	}
	for _, c := range ir {
		if got := c.a.Intersects(c.b); got != c.want {
			t.Errorf("%s: Intersects = %v, want %v", c.name, got, c.want)
		}
	}

	cr := []struct {
		name string
		big, small Rect
		want       bool
	}{
		{"contained", Rect{0, 0, 10, 10}, Rect{1, 1, 9, 9}, true},
		{"exact", Rect{0, 0, 10, 10}, Rect{0, 0, 10, 10}, true},
		{"protrude", Rect{0, 0, 10, 10}, Rect{1, 1, 11, 9}, false},
	}
	for _, c := range cr {
		if got := c.big.ContainsRect(c.small); got != c.want {
			t.Errorf("%s: ContainsRect = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSplitMembership(t *testing.T) {
	r := Rect{0, 0, 10, 10}
	kids, mx, my := r.Split()
	cases := []struct {
		name string
		p    Point
		want int
	}{
		{"sw interior", Point{1, 1, 0}, SW},
		{"se interior", Point{9, 1, 0}, SE},
		{"nw interior", Point{1, 9, 0}, NW},
		{"ne interior", Point{9, 9, 0}, NE},
		{"on vertical split goes east", Point{mx, 1, 0}, SE},
		{"on horizontal split goes north", Point{1, my, 0}, NW},
		{"split intersection goes NE", Point{mx, my, 0}, NE},
	}
	for _, c := range cases {
		home := -1
		for i := range kids {
			if kids[i].Contains(c.p) {
				if home != -1 {
					t.Fatalf("%s: point in two kids %d and %d", c.name, home, i)
				}
				home = i
			}
		}
		if home != c.want {
			t.Errorf("%s: home = %d, want %d", c.name, home, c.want)
		}
	}
}

func TestSplittableAndFinite(t *testing.T) {
	if !(Rect{0, 0, 10, 10}).Splittable(0, 48) {
		t.Fatal("normal rect should split")
	}
	if (Rect{0, 0, 10, 10}).Splittable(48, 48) {
		t.Fatal("depth limit must stop split")
	}
	tiny := math.Float64frombits(1)
	if (Rect{0, 0, tiny, tiny}).Splittable(0, 48) {
		t.Fatal("un-bisectable width must stop split")
	}
	zero := Rect{}
	if !zero.IsFinite() {
		t.Fatal("zero rect is finite")
	}
	if (Rect{0, 0, math.Inf(1), 1}).IsFinite() {
		t.Fatal("inf bounds not finite")
	}
	bad := []Point{{math.NaN(), 0, 0}, {0, math.Inf(-1), 0}, {math.Inf(1), 0, 0}}
	for _, p := range bad {
		if ValidFinitePoint(p) {
			t.Errorf("%v must be rejected", p)
		}
	}
}
