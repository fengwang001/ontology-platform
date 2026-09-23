// Package geom defines points and half-open rectangles on a 2D plane.
package geom

import "math"

// Point is a point with a caller-assigned unique ID.
type Point struct {
	X, Y float64
	ID   uint64
}

// Rect is a half-open rectangle [X0,X1) x [Y0,Y1).
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// Contains reports whether p lies inside the half-open rectangle.
// NaN coordinates never compare true, so NaN points are never contained.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X0 && p.X < r.X1 && p.Y >= r.Y0 && p.Y < r.Y1
}

// ContainsPointXY is Contains without requiring an ID.
func (r Rect) ContainsPointXY(x, y float64) bool {
	return x >= r.X0 && x < r.X1 && y >= r.Y0 && y < r.Y1
}

// Intersects reports whether two half-open rectangles share any point.
// Sharing only a boundary line does not count (both sides are open there).
func (r Rect) Intersects(o Rect) bool {
	return r.X0 < o.X1 && o.X0 < r.X1 && r.Y0 < o.Y1 && o.Y0 < r.Y1
}

// ContainsRect reports whether o is fully contained in r (boundaries may touch).
func (r Rect) ContainsRect(o Rect) bool {
	return o.X0 >= r.X0 && o.X1 <= r.X1 && o.Y0 >= r.Y0 && o.Y1 <= r.Y1
}

// MidX returns the x midpoint; MidY returns the y midpoint.
func (r Rect) MidX() float64 { return (r.X0 + r.X1) / 2 }

// MidY returns the y midpoint.
func (r Rect) MidY() float64 { return (r.Y0 + r.Y1) / 2 }

// IsFinite reports whether all four bounds are finite and non-NaN.
func (r Rect) IsFinite() bool {
	v := [...]float64{r.X0, r.Y0, r.X1, r.Y1}
	for _, f := range v {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	}
	return true
}

// ValidFinitePoint rejects NaN and infinite coordinates.
func ValidFinitePoint(p Point) bool {
	return !math.IsNaN(p.X) && !math.IsNaN(p.Y) &&
		!math.IsInf(p.X, 0) && !math.IsInf(p.Y, 0)
}

// Quadrant indices used by split cells: SW=0, SE=1, NW=2, NE=3.
const (
	SW = 0
	SE = 1
	NW = 2
	NE = 3
)

// Split returns the four sub-rectangles in order SW, SE, NW, NE and the
// midpoints. Callers must verify Splittable first.
func (r Rect) Split() (kids [4]Rect, mx, my float64) {
	mx, my = r.MidX(), r.MidY()
	kids[SW] = Rect{r.X0, r.Y0, mx, my}
	kids[SE] = Rect{mx, r.Y0, r.X1, my}
	kids[NW] = Rect{r.X0, my, mx, r.Y1}
	kids[NE] = Rect{mx, my, r.X1, r.Y1}
	return kids, mx, my
}

// Splittable reports whether both dimensions can still be bisected in
// floating point and the depth budget is not exhausted.
func (r Rect) Splittable(depth, maxDepth int) bool {
	if depth >= maxDepth {
		return false
	}
	mx, my := r.MidX(), r.MidY()
	if mx == r.X0 || mx == r.X1 || my == r.Y0 || my == r.Y1 {
		return false
	}
	return true
}
