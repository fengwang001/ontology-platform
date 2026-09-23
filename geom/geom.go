// Package geom defines points and axis-aligned rectangles on a plane,
// using the half-open convention [x0,x1) x [y0,y1) everywhere.
package geom

import "math"

// Point is a labeled point. ID lets callers distinguish coincident points.
type Point struct {
	ID uint64
	X  float64
	Y  float64
}

// Rect is a half-open rectangle [X0,X1) x [Y0,Y1).
// Degenerate rectangles (X0==X1 and/or Y0==Y1) are legal and contain no point.
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// Contains reports whether p is inside the half-open rectangle.
// +0 and -0 compare equal per IEEE 754.
func (r Rect) Contains(p Point) bool {
	return r.X0 <= p.X && p.X < r.X1 && r.Y0 <= p.Y && p.Y < r.Y1
}

// ContainsXY is Contains without constructing a Point.
func (r Rect) ContainsXY(x, y float64) bool {
	return r.X0 <= x && x < r.X1 && r.Y0 <= y && y < r.Y1
}

// Disjoint reports whether r and q share no point under half-open semantics.
// Touching edges are disjoint: [0,1) and [1,2) share no point.
func (r Rect) Disjoint(q Rect) bool {
	return r.X1 <= q.X0 || r.X0 >= q.X1 || r.Y1 <= q.Y0 || r.Y0 >= q.Y1
}

// Intersects reports whether the interiors+left edges overlap.
func (r Rect) Intersects(q Rect) bool { return !r.Disjoint(q) }

// FullyInside reports whether every point of r (half-open) is contained in q,
// i.e. r may be bulk-taken while answering a query q.
func (r Rect) FullyInside(q Rect) bool {
	return q.X0 <= r.X0 && r.X1 <= q.X1 && q.Y0 <= r.Y0 && r.Y1 <= q.Y1
}

// Mid splits r into four half-open quadrants around (mx,my).
// Order is SW, SE, NW, NE: points on x==mx go east, y==my go north.
func (r Rect) Mid() (mx, my float64) { return (r.X0 + r.X1) / 2, (r.Y0 + r.Y1) / 2 }

// Quadrants returns the four child rectangles (SW, SE, NW, NE).
// When a dimension is degenerate the midpoint equals that bound and the
// resulting half-open strips on the closed side are empty rectangles.
func (r Rect) Quadrants() [4]Rect {
	mx, my := r.Mid()
	return [4]Rect{
		{r.X0, r.Y0, mx, my},
		{mx, r.Y0, r.X1, my},
		{r.X0, my, mx, r.Y1},
		{mx, my, r.X1, r.Y1},
	}
}

// quadrant returns the child index (0=SW,1=SE,2=NW,3=NE) for a contained point.
func (r Rect) quadrant(p Point) int {
	mx, my := r.Mid()
	idx := 0
	if p.X >= mx {
		idx |= 1
	}
	if p.Y >= my {
		idx |= 2
	}
	return idx
}

// ContainsRect reports whether child is contained within r's closed bounds;
// used for structural validation when reading an index back from disk.
func (r Rect) ContainsRect(child Rect) (dimension int, ok bool) {
	switch {
	case child.X0 < r.X0 || child.X1 > r.X1:
		return 0, false
	case child.Y0 < r.Y0 || child.Y1 > r.Y1:
		return 1, false
	default:
		return -1, true
	}
}

// ValidPoint rejects NaN and Inf coordinates.
func ValidPoint(p Point) bool {
	return math.IsNaN(p.X) || math.IsNaN(p.Y) ||
		math.IsInf(p.X, 0) || math.IsInf(p.Y, 0)
}
