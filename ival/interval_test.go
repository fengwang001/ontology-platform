package ival_test

import (
	"errors"
	"testing"

	"ontology/ival"
)

func TestNew(t *testing.T) {
	cases := []struct {
		name   string
		l, r   int64
		wantOK bool
	}{
		{"normal", 1, 3, true},
		{"zero length", 2, 2, true},
		{"negative endpoints", -5, -1, true},
		{"invalid", 4, 2, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			iv, err := ival.New(c.l, c.r)
			if c.wantOK != (err == nil) {
				t.Fatalf("ok mismatch: got err=%v", err)
			}
			if !c.wantOK && !errors.Is(err, ival.ErrInvalidInterval) {
				t.Fatalf("want ErrInvalidInterval, got %v", err)
			}
			if c.wantOK && (iv.L != c.l || iv.R != c.r) {
				t.Fatalf("got %v", iv)
			}
		})
	}
}

func TestRelations(t *testing.T) {
	i := func(l, r int64) ival.Interval { return ival.Interval{L: l, R: r} }
	cases := []struct {
		name           string
		a, b           ival.Interval
		overlap        bool
		abut           bool
		interOK        bool
		unionOK        bool
		interL, interR int64
	}{
		{"disjoint", i(0, 1), i(2, 3), false, false, false, false, 0, 0},
		{"abutting", i(1, 3), i(3, 5), false, true, false, true, 0, 0},
		{"partial", i(1, 4), i(3, 5), true, false, true, true, 3, 4},
		{"contained", i(1, 9), i(3, 5), true, false, true, true, 3, 5},
		{"identical", i(2, 4), i(2, 4), true, false, true, true, 2, 4},
		{"zero-vs-overlap-range", i(2, 2), i(1, 5), false, false, false, false, 0, 0},
		{"zero-vs-zero", i(2, 2), i(2, 2), false, true, false, true, 0, 0},
		{"zero-vs-abut", i(2, 2), i(2, 3), false, true, false, true, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.a.Overlaps(c.b); got != c.overlap {
				t.Errorf("Overlaps=%v want %v", got, c.overlap)
			}
			if got := c.a.Abuts(c.b); got != c.abut {
				t.Errorf("Abuts=%v want %v", got, c.abut)
			}
			in, ok := c.a.Intersection(c.b)
			if ok != c.interOK || (ok && (in.L != c.interL || in.R != c.interR)) {
				t.Errorf("Intersection=%v,%v want %v,[%d,%d)", in, ok, c.interOK, c.interL, c.interR)
			}
			if _, ok := c.a.Union(c.b); ok != c.unionOK {
				t.Errorf("Union ok=%v want %v", ok, c.unionOK)
			}
		})
	}
}

func TestContains(t *testing.T) {
	cases := []struct {
		name string
		iv   ival.Interval
		x    int64
		want bool
	}{
		{"inside", ival.Interval{L: 1, R: 5}, 3, true},
		{"left end", ival.Interval{L: 1, R: 5}, 1, true},
		{"right end excluded", ival.Interval{L: 1, R: 5}, 5, false},
		{"before", ival.Interval{L: 1, R: 5}, 0, false},
		{"zero never 1", ival.Interval{L: 2, R: 2}, 2, false},
		{"zero never 2", ival.Interval{L: 2, R: 2}, 1, false},
		{"abut point belongs right", ival.Interval{L: 1, R: 3}, 3, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.iv.Contains(c.x); got != c.want {
				t.Fatalf("Contains(%d)=%v want %v", c.x, got, c.want)
			}
		})
	}
}
