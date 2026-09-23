// Package cell implements a single grid cell: a half-open rectangle that
// holds points while a leaf and references four children once split.
package cell

import "ontology/geom"

// Index selects one of the four quadrants.
const (
	BL = 0 // bottom-left
	BR = 1 // bottom-right
	TL = 2 // top-left
	TR = 3 // top-right
)

// Cell is one node of the quadtree.
type Cell struct {
	Bounds    geom.Rect
	Depth     int
	Points    []geom.Point // populated only on leaf cells
	Children  [4]*Cell     // populated only when Split is true, order BL,BR,TL,TR
	Split     bool
	Oversized bool // leaf that cannot split further but exceeds capacity
}

// New creates a root-style leaf cell.
func New(bounds geom.Rect, depth int) *Cell {
	return &Cell{Bounds: bounds, Depth: depth}
}

// IsLeaf reports whether c stores points directly.
func (c *Cell) IsLeaf() bool { return !c.Split }

// Contains delegates to the half-open bounds test.
func (c *Cell) Contains(p geom.Point) bool {
	return c.Bounds.ContainsPoint(p)
}

// Quadrant returns which child quadrant p belongs to after a split at
// mid. The half-open rule makes the split line belong to the
// right/upper child: x==mx -> right, y==my -> upper.
func Quadrant(p geom.Point, mx, my float64) int {
	right := !(p.X < mx)
	upper := !(p.Y < my)
	switch {
	case !right && !upper:
		return BL
	case right && !upper:
		return BR
	case !right && upper:
		return TL
	default:
		return TR
	}
}

// Mid returns the split lines.
func (c *Cell) Mid() (mx, my float64) {
	return (c.Bounds.X0 + c.Bounds.X1) / 2, (c.Bounds.Y0 + c.Bounds.Y1) / 2
}

// Splittable reports whether a split would produce strictly smaller,
// representable ranges on both axes. When a midpoint collapses onto an
// edge (float64 precision exhausted) no useful split is possible.
func (c *Cell) Splittable() bool {
	mx, my := c.Mid()
	return mx != c.Bounds.X0 && mx != c.Bounds.X1 &&
		my != c.Bounds.Y0 && my != c.Bounds.Y1
}
