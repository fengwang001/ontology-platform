// Package geom defines points and half-open rectangles for the grid index.
package geom

import "math"

// Point is an identified point on the plane.
type Point struct {
	ID uint64
	X  float64
	Y  float64
}

// Rect is the half-open rectangle [X0,X1) x [Y0,Y1).
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// Contains reports whether p belongs to the half-open rectangle.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X0 && p.X < r.X1 && p.Y >= r.Y0 && p.Y < r.Y1
}

// Intersects reports whether the two half-open rectangles share any point.
func (r Rect) Intersects(s Rect) bool {
	if r.Empty() || s.Empty() {
		return false
	}
	return r.X0 < s.X1 && s.X0 < r.X1 && r.Y0 < s.Y1 && s.Y0 < r.Y1
}

// ContainsRect reports whether s is fully contained in r (boundaries per half-open rules).
// Degenerate rectangles are contained only if both are empty (handled by callers).
func (r Rect) ContainsRect(s Rect) bool {
	return s.X0 >= r.X0 && s.X1 <= r.X1 && s.Y0 >= r.Y0 && s.Y1 <= r.Y1
}

// Empty reports whether the rectangle contains no points.
func (r Rect) Empty() bool { return r.X0 >= r.X1 || r.Y0 >= r.Y1 }

// ValidCoord reports whether v is usable as a coordinate (no NaN, no Inf).
func ValidCoord(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// ValidPoint reports whether the point has finite coordinates.
func ValidPoint(p Point) bool { return ValidCoord(p.X) && ValidCoord(p.Y) }
