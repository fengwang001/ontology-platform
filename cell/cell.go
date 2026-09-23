// Package cell implements a single grid cell: a half-open rectangle that
// holds points and may split into four children when over capacity.
package cell

import (
	"math"

	"ontology/geom"
)

// MaxDepth bounds recursion so that pathological inputs (e.g. many points at
// identical coordinates) cannot split forever.
const MaxDepth = 64

// Cell is a node of the split tree. A leaf keeps its points directly. After
// a split, Points is empty and the four quadrants own them instead.
type Cell struct {
	Bounds geom.Rect
	Depth  int
	Split  bool
	Points []geom.Point
	SW, SE *Cell // south (lower y) west/east
	NW, NE *Cell // north (upper y) west/east
}

// New creates a root cell covering b at depth 0.
func New(b geom.Rect) *Cell {
	return &Cell{Bounds: b}
}

// Leaf reports whether c currently stores points (has not split).
func (c *Cell) Leaf() bool { return !c.Split }

// Count returns the number of points in c's whole subtree.
func (c *Cell) Count() int {
	if c.Leaf() {
		return len(c.Points)
	}
	return c.SW.Count() + c.SE.Count() + c.NW.Count() + c.NE.Count()
}

// Quadrant returns the child that owns (x,y) under the half-open split rules:
// x>=mx goes east, y>=my goes north, so points on a split line land in exactly
// one child. It panics if (x,y) is outside c.
func (c *Cell) Quadrant(x, y float64) *Cell {
	mx, my := c.mid()
	switch {
	case x >= mx && y >= my:
		return c.NE
	case x >= mx:
		return c.SE
	case y >= my:
		return c.NW
	default:
		return c.SW
	}
}

// mid returns the split coordinates, avoiding overflow on huge magnitudes.
func (c *Cell) mid() (mx, my float64) {
	return midpoint(c.Bounds.X0, c.Bounds.X1), midpoint(c.Bounds.Y0, c.Bounds.Y1)
}

func midpoint(a, b float64) float64 {
	m := a + (b-a)/2
	if m < a || m > b { // fell outside due to precision: cannot subdivide
		return a
	}
	return m
}

// splittable reports whether both axes still admit a distinct midpoint and the
// depth limit has not been reached.
func (c *Cell) splittable() bool {
	if c.Depth >= MaxDepth {
		return false
	}
	mx, my := c.mid()
	xOK := mx != c.Bounds.X0 && mx != c.Bounds.X1
	yOK := my != c.Bounds.Y0 && my != c.Bounds.Y1
	return xOK && yOK
}

// TrySplit splits c into four children and rehomes every point when it is over
// capacity and subdivision is still possible. It returns true if a split
// happened. Saturated over-full leaves (identical coordinates / float64
// resolution / depth limit) keep their points and are never recursed into.
func (c *Cell) TrySplit(capacity int) bool {
	if c.Split || len(c.Points) <= capacity || !c.splittable() {
		return false
	}
	mx, my := c.mid()
	next := c.Depth + 1
	mk := func(b geom.Rect) *Cell { return &Cell{Bounds: b, Depth: next} }
	c.SW = mk(geom.Rect{X0: c.Bounds.X0, Y0: c.Bounds.Y0, X1: mx, Y1: my})
	c.SE = mk(geom.Rect{X0: mx, Y0: c.Bounds.Y0, X1: c.Bounds.X1, Y1: my})
	c.NW = mk(geom.Rect{X0: c.Bounds.X0, Y0: my, X1: mx, Y1: c.Bounds.Y1})
	c.NE = mk(geom.Rect{X0: mx, Y0: my, X1: c.Bounds.X1, Y1: c.Bounds.Y1})
	for _, p := range c.Points {
		c.Quadrant(p.X, p.Y).Points = append(c.Quadrant(p.X, p.Y).Points, p)
	}
	c.Points = nil
	c.Split = true
	return true
}

// Children returns the four children in fixed SW,SE,NW,NE order (nil for a
// leaf).
func (c *Cell) Children() [4]*Cell { return [4]*Cell{c.SW, c.SE, c.NW, c.NE} }

// EnsureMidFinite is a small guard used by callers building cells from disk.
func EnsureMidFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
