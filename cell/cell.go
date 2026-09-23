// Package cell implements the quadtree node: a half-open cell with an
// optional set of four children produced by overflow splitting.
package cell

import "ontology/geom"

// Child quadrant order: 0=lower-left, 1=lower-right, 2=upper-left, 3=upper-right.
type Cell struct {
	Bounds   geom.Rect
	Depth    int
	Points   []geom.Point
	Children [4]*Cell
}

// New creates a root-like cell with the given bounds and depth 0.
func New(bounds geom.Rect) *Cell { return &Cell{Bounds: bounds} }

// Leaf reports whether the cell has never been split.
func (c *Cell) Leaf() bool { return c.Children[0] == nil }

// MidX and MidY are the split coordinates.
func (c *Cell) MidX() float64 { return (c.Bounds.X0 + c.Bounds.X1) / 2 }
func (c *Cell) MidY() float64 { return (c.Bounds.Y0 + c.Bounds.Y1) / 2 }

// ChildFor returns the quadrant index that owns p under half-open rules.
func (c *Cell) ChildFor(p geom.Point) int {
	idx := 0
	if p.X >= c.MidX() {
		idx |= 1
	}
	if p.Y >= c.MidY() {
		idx |= 2
	}
	return idx
}

// canSplit reports whether geometry and depth permit a meaningful split.
func (c *Cell) canSplit(maxDepth int) bool {
	if c.Depth >= maxDepth {
		return false
	}
	mx, my := c.MidX(), c.MidY()
	b := c.Bounds
	return mx != b.X0 && mx != b.X1 && my != b.Y0 && my != b.Y1
}

func (c *Cell) childBounds(idx int) geom.Rect {
	mx, my := c.MidX(), c.MidY()
	b := c.Bounds
	if idx&1 != 0 {
		b.X0 = mx
	} else {
		b.X1 = mx
	}
	if idx&2 != 0 {
		b.Y0 = my
	} else {
		b.Y1 = my
	}
	return b
}

// Insert adds p, splitting the cell (recursively) when capacity is exceeded.
func (c *Cell) Insert(p geom.Point, capacity, maxDepth int) {
	if !c.Leaf() {
		c.Children[c.ChildFor(p)].Insert(p, capacity, maxDepth)
		return
	}
	c.Points = append(c.Points, p)
	if len(c.Points) <= capacity || !c.canSplit(maxDepth) {
		return
	}
	for idx := range c.Children {
		c.Children[idx] = &Cell{Bounds: c.childBounds(idx), Depth: c.Depth + 1}
	}
	moved := c.Points
	c.Points = nil
	for _, q := range moved {
		c.Children[c.ChildFor(q)].Insert(q, capacity, maxDepth)
	}
}

// RemoveAt deletes the point with id from the known owning leaf (descended by coords).
func (c *Cell) RemoveAt(id uint64) bool {
	if !c.Leaf() {
		for _, ch := range c.Children {
			if ch.RemoveAt(id) {
				return true
			}
		}
		return false
	}
	for i, p := range c.Points {
		if p.ID == id {
			c.Points = append(c.Points[:i], c.Points[i+1:]...)
			return true
		}
	}
	return false
}

// Count returns the number of points in the whole subtree.
func (c *Cell) Count() int {
	if !c.Leaf() {
		n := 0
		for _, ch := range c.Children {
			n += ch.Count()
		}
		return n
	}
	return len(c.Points)
}
