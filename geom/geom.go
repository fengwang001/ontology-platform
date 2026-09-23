// Package geom defines points and axis-aligned, half-open rectangles.
package geom

// Point is a labeled point in the plane. ID is assigned by the caller.
type Point struct {
	ID uint64
	X  float64
	Y  float64
}

// Rect is a half-open rectangle [X0,X1) x [Y0,Y1). Zero-width or
// zero-height rectangles are valid and contain no point.
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// Contains reports whether p belongs to the half-open rectangle.
// NaN coordinates never belong to any rectangle.
func (r Rect) Contains(p Point) bool {
	return r.X0 <= p.X && p.X < r.X1 && r.Y0 <= p.Y && p.Y < r.Y1
}

// Disjoint reports whether r and s share no point under half-open semantics.
func (r Rect) Disjoint(s Rect) bool {
	return r.X1 <= s.X0 || s.X1 <= r.X0 || r.Y1 <= s.Y0 || s.Y1 <= r.Y0
}

// ContainsRect reports whether r covers the whole half-open rectangle inner.
// Because both sides are half-open, p with p.X < inner.X1 <= r.X1 is always
// inside r; equality on the shared upper edge is therefore safe.
func (r Rect) ContainsRect(inner Rect) bool {
	return r.X0 <= inner.X0 && inner.X1 <= r.X1 &&
		r.Y0 <= inner.Y0 && inner.Y1 <= r.Y1
}

// Finite reports whether x is neither NaN nor an infinity.
func Finite(x float64) bool {
	// NaN is the only value unequal to itself; infinities exceed MaxFloat.
	return x == x && -maxFloat <= x && x <= maxFloat
}

const maxFloat = 1.7976931348623157e+308
