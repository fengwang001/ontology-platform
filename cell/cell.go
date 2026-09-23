// Package cell implements the quadtree node: a half-open rectangular bucket
// that splits into four children when it holds more than its capacity.
package cell

import "ontology/geom"

// MaxDepth bounds recursion as a second line of defense against unsplittable
// float64 intervals (see DESIGN.md section 2.1).
const MaxDepth = 100

// Cell is one node of the split grid. A leaf owns Points; a split node owns
// Children and Points is empty. Saturated leaves may exceed capacity: their
// interval is no longer representably divisible or MaxDepth was reached.
type Cell struct {
	Bounds    geom.Rect
	Depth     int
	Split     bool
	Saturated bool
	Points    []geom.Point
	Children  [4]*Cell
}

// New creates a root cell covering b at depth 0.
func New(b geom.Rect) *Cell {
	return &Cell{Bounds: b}
}

// ChildIndex returns the quadrant code (0=SW,1=SE,2=NW,3=NE) of p in c.
// A point on a split line goes to the lower-index side: x==mx goes west,
// y==my goes south; the exact center lands in SW (index 0).
func (c *Cell) ChildIndex(p geom.Point) int {
	mx := (c.Bounds.X0 + c.Bounds.X1) / 2
	my := (c.Bounds.Y0 + c.Bounds.Y1) / 2
	q := 0
	if p.X >= mx {
		q |= 1
	}
	if p.Y >= my {
		q |= 2
	}
	return q
}

// CanSplit reports whether a split is mathematically possible at the
// current depth: depth below the cap and at least one axis still divisible
// in float64 (the midpoint rounds strictly inside the interval).
func (c *Cell) CanSplit() bool {
	if c.Depth >= MaxDepth {
		return false
	}
	b := c.Bounds
	mx, my := (b.X0+b.X1)/2, (b.Y0+b.Y1)/2
	xDiv := mx > b.X0 && mx < b.X1
	yDiv := my > b.Y0 && my < b.Y1
	return xDiv || yDiv
}

// midRect builds the child rectangle for quadrant q.
func (c *Cell) midRect(mx, my float64, q int) geom.Rect {
	b := c.Bounds
	x0, x1, y0, y1 := b.X0, mx, b.Y0, my
	if q&1 != 0 {
		x0, x1 = mx, b.X1
	}
	if q&2 != 0 {
		y0, y1 = my, b.Y1
	}
	return geom.Rect{X0: x0, Y0: y0, X1: x1, Y1: y1}
}

// DoSplit moves every point of an overfull leaf into four fresh children.
// It is a no-op on already split or unsplittable cells; the caller marks
// such leaves Saturated when they remain over capacity.
func (c *Cell) DoSplit() {
	if c.Split || !c.CanSplit() {
		return
	}
	b := c.Bounds
	mx, my := (b.X0+b.X1)/2, (b.Y0+b.Y1)/2
	for q := 0; q < 4; q++ {
		c.Children[q] = &Cell{Bounds: c.midRect(mx, my, q), Depth: c.Depth + 1}
	}
	for _, p := range c.Points {
		q := c.ChildIndex(p)
		c.Children[q].Points = append(c.Children[q].Points, p)
	}
	c.Points = nil
	c.Split = true
}

// Count returns the number of points in the whole subtree.
func (c *Cell) Count() int {
	if !c.Split {
		return len(c.Points)
	}
	n := 0
	for _, ch := range c.Children {
		n += ch.Count()
	}
	return n
}

// NumCells returns the number of cells (nodes) in the subtree.
func (c *Cell) NumCells() int {
	if !c.Split {
		return 1
	}
	n := 1
	for _, ch := range c.Children {
		n += ch.NumCells()
	}
	return n
}
