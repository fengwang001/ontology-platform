package geom

import (
	"math"
	"testing"
)

// 九种点位置对格 [0,4)×[0,4) 的归属（左闭右开）。
func TestContains(t *testing.T) {
	r := Rect{0, 0, 4, 4}
	cases := []struct {
		name string
		p    Point
		want bool
	}{
		{"格内", Point{2, 2}, true},
		{"左边界", Point{0, 2}, true},
		{"右边界", Point{4, 2}, false},
		{"下边界", Point{2, 0}, true},
		{"上边界", Point{2, 4}, false},
		{"左下角", Point{0, 0}, true},
		{"右下角", Point{4, 0}, false},
		{"左上角", Point{0, 4}, false},
		{"右上角", Point{4, 4}, false},
		{"分裂线x", Point{2, 1}, true},
		{"负零等于零", Point{math.Copysign(0, -1), 2}, true},
	}
	for _, c := range cases {
		if got := r.Contains(c.p); got != c.want {
			t.Errorf("%s: Contains(%v)=%v want %v", c.name, c.p, got, c.want)
		}
	}
}

func TestIntersectsAndContainsRect(t *testing.T) {
	r := Rect{0, 0, 4, 4}
	cases := []struct {
		name    string
		o       Rect
		inter   bool
		contain bool
	}{
		{"完全内含", Rect{1, 1, 2, 2}, true, true},
		{"部分相交", Rect{3, 3, 5, 5}, true, false},
		{"右边相离", Rect{4, 0, 6, 4}, false, false},
		{"上边相离", Rect{0, 4, 4, 6}, false, false},
		{"角相离", Rect{4, 4, 8, 8}, false, false},
		{"退化线", Rect{2, 0, 2, 4}, false, false},
		{"退化点", Rect{2, 2, 2, 2}, false, false},
		{"自身", Rect{0, 0, 4, 4}, true, true},
	}
	for _, c := range cases {
		if got := r.Intersects(c.o); got != c.inter {
			t.Errorf("%s: Intersects=%v want %v", c.name, got, c.inter)
		}
		if got := r.ContainsRect(c.o); got != c.contain {
			t.Errorf("%s: ContainsRect=%v want %v", c.name, got, c.contain)
		}
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		name string
		p    Point
		want bool
	}{
		{"普通", Point{1, 2}, true},
		{"NaN-x", Point{math.NaN(), 1}, false},
		{"NaN-y", Point{1, math.NaN()}, false},
		{"+Inf", Point{math.Inf(1), 1}, false},
		{"-Inf", Point{1, math.Inf(-1)}, false},
	}
	for _, c := range cases {
		if got := c.p.Valid(); got != c.want {
			t.Errorf("%s: Valid=%v want %v", c.name, got, c.want)
		}
	}
}
