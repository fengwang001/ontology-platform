package cell

import (
	"ontology/geom"
	"testing"
)

const (
	testCap   = 4
	testDepth = 32
)

func find(c *Cell, id uint64) (geom.Point, bool) {
	if !c.Leaf() {
		for _, ch := range c.Children {
			if p, ok := find(ch, id); ok {
				return p, true
			}
		}
		return geom.Point{}, false
	}
	for _, p := range c.Points {
		if p.ID == id {
			return p, true
		}
	}
	return geom.Point{}, false
}

func TestChildForHalfOpen(t *testing.T) {
	c := New(geom.Rect{0, 0, 10, 10})
	cases := []struct {
		name string
		p    geom.Point
		want int
	}{
		{"lower-left", geom.Point{1, 1, 1}, 0},
		{"lower-right", geom.Point{2, 9, 1}, 1},
		{"upper-left", geom.Point{3, 1, 9}, 2},
		{"upper-right", geom.Point{4, 9, 9}, 3},
		{"split-x-goes-right", geom.Point{5, 5, 1}, 1},
		{"split-y-goes-up", geom.Point{6, 1, 5}, 2},
		{"split-center-goes-upper-right", geom.Point{7, 5, 5}, 3},
	}
	for _, tc := range cases {
		if got := c.ChildFor(tc.p); got != tc.want {
			t.Errorf("%s: ChildFor = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestInsertSplitsAndPreserves(t *testing.T) {
	root := New(geom.Rect{0, 0, 10, 10})
	pts := []geom.Point{
		{1, 1, 1}, {2, 9, 1}, {3, 1, 9}, {4, 9, 9}, {5, 5, 5},
	}
	for _, p := range pts {
		root.Insert(p, testCap, testDepth)
	}
	if root.Leaf() {
		t.Fatal("root should have split after exceeding capacity")
	}
	if got := root.Count(); got != len(pts) {
		t.Fatalf("total count = %d, want %d", got, len(pts))
	}
	for _, p := range pts {
		if _, ok := find(root, p.ID); !ok {
			t.Errorf("point %d lost after split", p.ID)
		}
	}
	seen := map[uint64]int{}
	var walk func(*Cell)
	walk = func(c *Cell) {
		if c.Leaf() {
			for _, p := range c.Points {
				seen[p.ID]++
			}
			return
		}
		for _, ch := range c.Children {
			walk(ch)
		}
	}
	walk(root)
	for _, p := range pts {
		if seen[p.ID] != 1 {
			t.Errorf("point %d appears %d times, want exactly 1", p.ID, seen[p.ID])
		}
	}
}

func TestIdenticalPointsTerminate(t *testing.T) {
	root := New(geom.Rect{0, 0, 10, 10})
	const n = 1000
	for i := range n {
		root.Insert(geom.Point{uint64(i + 1), 7, 7}, testCap, testDepth)
	}
	if got := root.Count(); got != n {
		t.Fatalf("count = %d, want %d", got, n)
	}
	leafCount := 0
	maxDepth := 0
	var walk func(*Cell)
	walk = func(c *Cell) {
		if c.Leaf() {
			leafCount++
			if c.Depth > maxDepth {
				maxDepth = c.Depth
			}
			return
		}
		for _, ch := range c.Children {
			walk(ch)
		}
	}
	walk(root)
	if maxDepth > testDepth {
		t.Fatalf("depth %d exceeds cap %d", maxDepth, testDepth)
	}
	if _, ok := find(root, 1); !ok {
		t.Fatal("identical point not found after overflow leaf")
	}
	if _, ok := find(root, n); !ok {
		t.Fatal("last identical point not found")
	}
}

func TestRemove(t *testing.T) {
	root := New(geom.Rect{0, 0, 10, 10})
	for i := 1; i <= 10; i++ {
		root.Insert(geom.Point{uint64(i), float64(i), float64(i)}, testCap, testDepth)
	}
	cases := []struct {
		name string
		id   uint64
		ok   bool
		left int
	}{
		{"remove-existing", 3, true, 9},
		{"remove-again", 3, false, 9},
		{"remove-another", 10, true, 8},
	}
	for _, tc := range cases {
		if ok := root.RemoveAt(tc.id); ok != tc.ok {
			t.Errorf("%s: RemoveAt = %v, want %v", tc.name, ok, tc.ok)
		}
		if got := root.Count(); got != tc.left {
			t.Errorf("%s: count = %d, want %d", tc.name, got, tc.left)
		}
		if _, ok := find(root, tc.id); ok {
			t.Errorf("%s: removed point %d still present", tc.name, tc.id)
		}
	}
}
