// Package nbr provides exact integer squared-distance geometry predicates.
package nbr

// Point is a planar point with integer coordinates.
type Point struct {
	X int
	Y int
}

// Dist2 returns the exact squared Euclidean distance between p and q.
func Dist2(p, q Point) int64 {
	dx := int64(q.X) - int64(p.X)
	dy := int64(q.Y) - int64(p.Y)
	return dx*dx + dy*dy
}

// Inside reports whether a point at squared distance d2 lies strictly
// inside a circle whose squared radius is r2 (d2 < r2).
func Inside(d2, r2 int64) bool { return d2 < r2 }
