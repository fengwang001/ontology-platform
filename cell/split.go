package cell

import "ontology/geom"

// Split turns c into an internal node: four half-open children are
// created and every stored point is pushed down exactly once.
//
// The caller must check Splittable before calling. Points are
// redistributed with the half-open ownership rule, so each point lands
// in exactly one child and total point count is preserved.
func (c *Cell) Split() {
	mx, my := c.Mid()
	b := c.Bounds
	rects := [4]geom.Rect{
		{X0: b.X0, Y0: b.Y0, X1: mx, Y1: my}, // BL
		{X0: mx, Y0: b.Y0, X1: b.X1, Y1: my}, // BR
		{X0: b.X0, Y0: my, X1: mx, Y1: b.Y1}, // TL
		{X0: mx, Y0: my, X1: b.X1, Y1: b.Y1}, // TR
	}
	d := c.Depth + 1
	for i := range c.Children {
		c.Children[i] = &Cell{Bounds: rects[i], Depth: d}
	}
	for _, p := range c.Points {
		q := Quadrant(p, mx, my)
		c.Children[q].Points = append(c.Children[q].Points, p)
	}
	c.Points = nil
	c.Split = true
}

// Count returns the number of points in the whole subtree.
func (c *Cell) Count() int {
	if c.IsLeaf() {
		return len(c.Points)
	}
	n := 0
	for _, ch := range c.Children {
		n += ch.Count()
	}
	return n
}

// NumCells counts every node in the subtree.
func (c *Cell) NumCells() int {
	n := 1
	if !c.IsLeaf() {
		for _, ch := range c.Children {
			n += ch.NumCells()
		}
	}
	return n
}
