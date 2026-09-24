package row

import (
	"errors"
	"math"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		name string
		a, b Row
		want int
	}{
		{"score less", Row{1, "a"}, Row{2, "a"}, -1},
		{"score greater", Row{2, "a"}, Row{1, "a"}, 1},
		{"equal score id less", Row{1, "a"}, Row{1, "b"}, -1},
		{"equal score id greater", Row{1, "b"}, Row{1, "a"}, 1},
		{"same key", Row{1, "a"}, Row{1, "a"}, 0},
		{"negative before positive", Row{-2, "z"}, Row{-1, "a"}, -1},
		{"negative ordering", Row{-1, "a"}, Row{-2, "a"}, 1},
		{"zero equal then id", Row{0, "b"}, Row{0, "a"}, 1},
		{"neg zero before pos zero", Row{math.Copysign(0, -1), "a"}, Row{0, "a"}, -1},
		{"large values", Row{1e308, "a"}, Row{1e-308, "b"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compare(tc.a, tc.b); got != tc.want {
				t.Fatalf("Compare = %d, want %d", got, tc.want)
			}
			if tc.want != 0 && Compare(tc.b, tc.a) != -tc.want {
				t.Fatalf("Compare not antisymmetric")
			}
		})
	}
}

func TestPredicates(t *testing.T) {
	a := Row{1, "a"}
	b := Row{1, "b"}
	pairs := []struct {
		x, y   Row
		less   bool
		after  bool
		before bool
	}{
		{a, b, true, false, true},
		{b, a, false, true, false},
		{a, a, false, false, false},
		{Row{0, "z"}, a, true, false, true},
	}
	for _, p := range pairs {
		if Less(p.x, p.y) != p.less {
			t.Fatalf("Less(%v,%v)=%v want %v", p.x, p.y, Less(p.x, p.y), p.less)
		}
		if After(p.x, p.y) != p.after {
			t.Fatalf("After(%v,%v)=%v want %v", p.x, p.y, After(p.x, p.y), p.after)
		}
		if Before(p.x, p.y) != p.before {
			t.Fatalf("Before(%v,%v)=%v want %v", p.x, p.y, Before(p.x, p.y), p.before)
		}
	}
}

func TestNewAndValid(t *testing.T) {
	cases := []struct {
		score float64
		id    string
		ok    bool
	}{
		{1, "x", true},
		{-3.5, "id-1", true},
		{math.NaN(), "x", false},
		{1, "", false},
		{math.NaN(), "", false},
	}
	for _, tc := range cases {
		r, err := New(tc.score, tc.id)
		if tc.ok {
			if err != nil || !r.Valid() {
				t.Fatalf("New(%v,%q) = %v,%v want ok", tc.score, tc.id, r, err)
			}
			continue
		}
		if !errors.Is(err, ErrInvalid) || r.Valid() {
			t.Fatalf("New(%v,%q) = %v,%v want ErrInvalid/invalid", tc.score, tc.id, r, err)
		}
	}
}
