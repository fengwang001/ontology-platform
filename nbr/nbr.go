// Package nbr holds the exact distance arithmetic and the disk predicate used
// everywhere else. It depends on no other package in the module.
package nbr

// Point is a site or query location with integer coordinates.
type Point struct {
	X int
	Y int
}

// Dist2 returns the exact squared Euclidean distance between p and q:
// (p.X-q.X)^2 + (p.Y-q.Y)^2, computed in int64 with no square root.
// With |coords| <= 1e4 the result is at most 8e8, so it never overflows.
func Dist2(p, q Point) int64 {
	dx := int64(p.X) - int64(q.X)
	dy := int64(p.Y) - int64(q.Y)
	return dx*dx + dy*dy
}

// Inside reports whether a point at squared distance d2 lies strictly inside a
// disk whose squared radius is r2. Points exactly on the circle (d2 == r2)
// are NOT inside.
func Inside(d2, r2 int64) bool {
	return d2 < r2
}
