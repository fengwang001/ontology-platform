package ival

import "testing"

func iv(lo, hi int64) Interval { return Interval{Lo: lo, Hi: hi} }

func TestOverlaps(t *testing.T) {
	cases := []struct {
		name string
		a, b Interval
		want bool
	}{
		{"disjoint", iv(1, 2), iv(3, 4), false},
		{"disjoint-rev", iv(3, 4), iv(1, 2), false},
		{"touching", iv(1, 3), iv(3, 5), false},
		{"touching-rev", iv(3, 5), iv(1, 3), false},
		{"partial", iv(1, 4), iv(3, 5), true},
		{"contains", iv(1, 5), iv(2, 3), true},
		{"contained", iv(2, 3), iv(1, 5), true},
		{"identical", iv(2, 4), iv(2, 4), true},
		{"zero-vs-containing", iv(2, 2), iv(1, 5), false},
		{"zero-vs-self", iv(2, 2), iv(2, 2), false},
		{"zero-vs-touching", iv(2, 2), iv(2, 5), false},
	}
	for _, c := range cases {
		if got := Overlaps(c.a, c.b); got != c.want {
			t.Errorf("%s: Overlaps(%v,%v)=%v want %v", c.name, c.a, c.b, got, c.want)
		}
		if got := Overlaps(c.b, c.a); got != c.want {
			t.Errorf("%s: Overlaps(%v,%v)=%v want %v (symmetry)", c.name, c.b, c.a, got, c.want)
		}
	}
}

func TestTouches(t *testing.T) {
	cases := []struct {
		a, b Interval
		want bool
	}{
		{iv(1, 3), iv(3, 5), true},
		{iv(3, 5), iv(1, 3), true},
		{iv(1, 4), iv(3, 5), false},
		{iv(1, 2), iv(3, 4), false},
	}
	for i, c := range cases {
		if got := Touches(c.a, c.b); got != c.want {
			t.Errorf("case %d: Touches=%v want %v", i, got, c.want)
		}
	}
}

func TestContains(t *testing.T) {
	cases := []struct {
		i    Interval
		p    int64
		want bool
	}{
		{iv(1, 3), 1, true},  // left closed
		{iv(1, 3), 3, false}, // right open
		{iv(1, 3), 2, true},  // interior
		{iv(3, 5), 3, true},  // touching point belongs to the right interval
		{iv(2, 2), 2, false}, // empty interval contains nothing
		{iv(1, 3), 0, false}, // left of interval
	}
	for i, c := range cases {
		if got := c.i.Contains(c.p); got != c.want {
			t.Errorf("case %d: %v.Contains(%d)=%v want %v", i, c.i, c.p, got, c.want)
		}
	}
}

func TestIntersectUnion(t *testing.T) {
	cases := []struct {
		a, b  Interval
		inter Interval
		union Interval
	}{
		{iv(1, 4), iv(3, 5), iv(3, 4), iv(1, 5)},
		{iv(1, 3), iv(3, 5), iv(3, 3), iv(1, 5)}, // touching: empty intersection
		{iv(1, 2), iv(3, 4), iv(3, 3), iv(1, 4)}, // disjoint: empty intersection
		{iv(1, 5), iv(2, 3), iv(2, 3), iv(1, 5)}, // containment
		{iv(2, 2), iv(1, 5), iv(2, 2), iv(1, 5)}, // empty stays empty
	}
	for i, c := range cases {
		if got := Intersect(c.a, c.b); got != c.inter {
			t.Errorf("case %d: Intersect=%v want %v", i, got, c.inter)
		}
		if got := Union(c.a, c.b); got != c.union {
			t.Errorf("case %d: Union=%v want %v", i, got, c.union)
		}
		if got := Intersect(c.a, c.b); got.Empty() != !Overlaps(c.a, c.b) {
			t.Errorf("case %d: empty intersection disagrees with Overlaps", i)
		}
	}
}

func TestValidAndCompare(t *testing.T) {
	if iv(3, 1).Valid() {
		t.Error("Lo > Hi must be invalid")
	}
	if !iv(2, 2).Valid() || iv(2, 2).Empty() != true {
		t.Error("zero-length interval must be valid and empty")
	}
	ordered := []Interval{
		{Lo: 1, Hi: 2}, {Lo: 1, Hi: 3}, {Lo: 1, Hi: 3, Payload: "a"},
		{Lo: 1, Hi: 3, Payload: "b"}, {Lo: 2, Hi: 2},
	}
	for i := 0; i < len(ordered); i++ {
		for j := 0; j < len(ordered); j++ {
			got := Compare(ordered[i], ordered[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Errorf("Compare(%v,%v)=%d want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}
