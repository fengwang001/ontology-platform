package cell

import (
	"testing"

	"ontology/geom"
)

func TestChildIndexSplitLineOwnership(t *testing.T) {
	c := New(geom.Rect{X0: 0, Y0: 0, X1: 10, Y1: 10})
	// midpoint is (5,5); index: bit1 = east, bit2 = north.
	cases := []struct {
		name string
		p    geom.Point
		want int
	}{
		{"interior SW", geom.Point{X: 1, Y: 1}, 0},
		{"interior SE", geom.Point{X: 9, Y: 1}, 1},
		{"interior NW", geom.Point{X: 1, Y: 9}, 2},
		{"interior NE", geom.Point{X: 9, Y: 9}, 3},
		{"on vertical line goes west-south", geom.Point{X: 5, Y: 1}, 0},
		{"on vertical line goes west-north", geom.Point{X: 5, Y: 9}, 2},
		{"on horizontal line goes south-west", geom.Point{X: 1, Y: 5}, 0},
		{"on horizontal line goes south-east", geom.Point{X: 9, Y: 5}, 1},
		{"exact center goes SW", geom.Point{X: 5, Y: 5}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.ChildIndex(tc.p); got != tc.want {
				t.Fatalf("ChildIndex = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDoSplitRelocatesAllPoints(t *testing.T) {
	c := New(geom.Rect{X0: 0, Y0: 0, X1: 10, Y1: 10})
	pts := []geom.Point{
		{ID: 1, X: 5, Y: 5}, // center -> SW
		{ID: 2, X: 5, Y: 6}, // vertical split line -> NW
		{ID: 3, X: 6, Y: 5}, // horizontal split line -> SE
		{ID: 4, X: 9, Y: 9}, // NE
		{ID: 5, X: 1, Y: 1}, // SW
	}
	c.Points = append(c.Points, pts...)
	before := c.Count()
	c.DoSplit()
	if !c.Split || len(c.Points) != 0 {
		t.Fatal("split node must be marked and emptied")
	}
	if got := c.Count(); got != before {
		t.Fatalf("count after split = %d, want %d", got, before)
	}
	wantCounts := [4]int{2, 1, 1, 1} // SW,SE,NW,NE
	var got [4]int
	for i, ch := range c.Children {
		got[i] = len(ch.Points)
	}
	if got != wantCounts {
		t.Fatalf("child counts = %v, want %v", got, wantCounts)
	}
}

func TestCanSplitTermination(t *testing.T) {
	cases := []struct {
		name  string
		b     geom.Rect
		depth int
		can   bool
	}{
		{"normal", geom.Rect{X0: 0, Y0: 0, X1: 10, Y1: 10}, 0, true},
		{"max depth", geom.Rect{X0: 0, Y0: 0, X1: 10, Y1: 10}, MaxDepth, false},
		{"point width on both axes", geom.Rect{X0: 1, Y0: 1, X1: 1, Y1: 1}, 0, false},
		{"minimal float cell both axes", geom.Rect{
			X0: 1.0, Y0: 1.0, X1: 1.0 + 1e-16, Y1: 1.0 + 1e-16}, 5, false},
		{"one axis collapsed still splits", geom.Rect{X0: 5, Y0: 0, X1: 5, Y1: 10}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Cell{Bounds: tc.b, Depth: tc.depth}
			if got := c.CanSplit(); got != tc.can {
				t.Fatalf("CanSplit = %v, want %v", got, tc.can)
			}
		})
	}
}

func TestDoSplitNoopWhenUnsplittable(t *testing.T) {
	c := New(geom.Rect{X0: 1, Y0: 1, X1: 1, Y1: 1})
	c.Points = []geom.Point{{ID: 1, X: 1, Y: 1}}
	c.DoSplit()
	if c.Split || len(c.Points) != 1 {
		t.Fatal("unsplittable leaf must keep its points and not split")
	}
}
