// Package poly provides integer-coordinate geometry predicates.
package poly

import "sort"

// Point is an integer-lattice vertex (|X|,|Y| <= 1e4 on valid input).
type Point struct{ X, Y int }

// Orient returns the sign of the cross product (b-a) x (c-a).
func Orient(a, b, c Point) int {
	cp := int64(b.X-a.X)*int64(c.Y-a.Y) - int64(b.Y-a.Y)*int64(c.X-a.X)
	if cp > 0 {
		return 1
	}
	if cp < 0 {
		return -1
	}
	return 0
}

// OnSeg reports whether p lies on the closed segment [a,b].
func OnSeg(a, b, p Point) bool {
	return Orient(a, b, p) == 0 &&
		min(a.X, b.X) <= p.X && p.X <= max(a.X, b.X) &&
		min(a.Y, b.Y) <= p.Y && p.Y <= max(a.Y, b.Y)
}

// PointInPoly reports strict containment by ray casting; boundary excluded.
func PointInPoly(p Point, q []Point) bool {
	inside := false
	for i := range q {
		a, b := q[i], q[(i+1)%len(q)]
		if OnSeg(a, b, p) {
			return false
		}
		if (a.Y > p.Y) != (b.Y > p.Y) {
			xiNum := int64(a.X)*int64(b.Y-p.Y) + int64(b.X)*int64(p.Y-a.Y)
			den := int64(b.Y - a.Y)
			lhs := int64(p.X) * den
			if (den > 0 && lhs < xiNum) || (den < 0 && lhs > xiNum) {
				inside = !inside
			}
		}
	}
	return inside
}

// SegIntersect returns the open-segment crossing; endpoint touches and
// collinear overlaps yield false.
func SegIntersect(a, b, c, d Point) (Point, bool) {
	d1, d2 := Orient(c, d, a), Orient(c, d, b)
	d3, d4 := Orient(a, b, c), Orient(a, b, d)
	if !((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) ||
		!((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0)) {
		return Point{}, false
	}
	den := int64(b.X-a.X)*int64(d.Y-c.Y) - int64(b.Y-a.Y)*int64(d.X-c.X)
	tNum := int64(c.X-a.X)*int64(d.Y-c.Y) - int64(c.Y-a.Y)*int64(d.X-c.X)
	if den < 0 {
		den, tNum = -den, -tNum
	}
	return Point{X: int((int64(a.X)*den + int64(b.X-a.X)*tNum) / den),
		Y: int((int64(a.Y)*den + int64(b.Y-a.Y)*tNum) / den)}, true
}

// Area2 returns twice the signed shoelace area (positive when CCW).
func Area2(q []Point) int {
	s := int64(0)
	for i := range q {
		a, b := q[i], q[(i+1)%len(q)]
		s += int64(a.X)*int64(b.Y) - int64(a.Y)*int64(b.X)
	}
	return int(s)
}

// OnBoundary reports whether p lies on any edge of q.
func OnBoundary(p Point, q []Point) bool {
	for i := range q {
		if OnSeg(q[i], q[(i+1)%len(q)], p) {
			return true
		}
	}
	return false
}

// Scaled returns a copy of q with both coordinates multiplied by f.
func Scaled(q []Point, f int) []Point {
	s := make([]Point, len(q))
	for i, p := range q {
		s[i] = Point{X: f * p.X, Y: f * p.Y}
	}
	return s
}

// Dist2 returns the squared distance between u and v.
func Dist2(u, v Point) int { return (u.X-v.X)*(u.X-v.X) + (u.Y-v.Y)*(u.Y-v.Y) }

// OrderAlong sorts nodes by distance from o and drops repeated points.
func OrderAlong(e []Point, o Point) []Point {
	sort.Slice(e, func(i, j int) bool { return Dist2(e[i], o) < Dist2(e[j], o) })
	f := e[:1]
	for _, p := range e[1:] {
		if p != f[len(f)-1] {
			f = append(f, p)
		}
	}
	return f
}
