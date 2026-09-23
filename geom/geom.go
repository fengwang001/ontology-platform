// Package geom defines points and half-open rectangles with the
// boundary semantics used by the splittable grid index.
package geom

import "math"

// Point is an identified planar point. ID lets callers distinguish and
// delete points that share identical coordinates.
type Point struct {
	ID uint64
	X  float64
	Y  float64
}

// Rect is a half-open rectangle [X0,X1) x [Y0,Y1).
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// ContainsPoint reports whether p lies in the half-open rectangle.
// Left and bottom edges belong to the rectangle; right and top do not.
// +0.0 and -0.0 compare equal under IEEE 754 and need no special case.
func (r Rect) ContainsPoint(p Point) bool {
	return r.X0 <= p.X && p.X < r.X1 && r.Y0 <= p.Y && p.Y < r.Y1
}

// Intersects reports whether the interiors/touching half-open ranges
// overlap. Equality on an edge is NOT an intersection (both ranges are
// half-open), so a degenerate rectangle never intersects a cell.
func (r Rect) Intersects(o Rect) bool {
	return r.X0 < o.X1 && o.X0 < r.X1 && r.Y0 < o.Y1 && o.Y0 < r.Y1
}

// ContainsRect reports whether o is fully inside r, edges included.
func (r Rect) ContainsRect(o Rect) bool {
	return r.X0 <= o.X0 && o.X1 <= r.X1 && r.Y0 <= o.Y0 && o.Y1 <= r.Y1
}

// ValidCoord rejects NaN and Inf coordinates.
func ValidCoord(v float64) bool {
	return v == v && !math.IsInf(v, 0)
}

// ValidRect reports whether r has finite, ordered bounds.
func ValidRect(r Rect) bool {
	return ValidCoord(r.X0) && ValidCoord(r.Y0) &&
		ValidCoord(r.X1) && ValidCoord(r.Y1) &&
		r.X0 <= r.X1 && r.Y0 <= r.Y1
}
