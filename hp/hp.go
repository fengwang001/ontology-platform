// Package hp defines half-plane predicates and exact rational geometry.
// A half-plane is the integer inequality A*x + B*y <= C; points on the
// boundary (A*x + B*y == C) count as inside.
package hp

import "math/big"

// HalfPlane is the inequality A*x + B*y <= C.
type HalfPlane struct {
	A, B, C int64
}

// Point is a point with exact rational coordinates.
type Point struct {
	X, Y *big.Rat
}

// Pt builds a Point from integers.
func Pt(x, y int64) Point {
	return Point{new(big.Rat).SetInt64(x), new(big.Rat).SetInt64(y)}
}

// Side returns A*x + B*y - C; sign <= 0 means p is inside h.
func Side(h HalfPlane, p Point) *big.Rat {
	r := new(big.Rat).Mul(new(big.Rat).SetInt64(h.A), p.X)
	r.Add(r, new(big.Rat).Mul(new(big.Rat).SetInt64(h.B), p.Y))
	return r.Sub(r, new(big.Rat).SetInt64(h.C))
}

// Inside reports whether p satisfies h (boundary included).
func Inside(h HalfPlane, p Point) bool {
	return Side(h, p).Sign() <= 0
}

// Intersect returns the intersection of h's boundary line with the
// segment p-q. ok is false when the segment is parallel to the line.
func Intersect(h HalfPlane, p, q Point) (Point, bool) {
	dx := new(big.Rat).Sub(q.X, p.X)
	dy := new(big.Rat).Sub(q.Y, p.Y)
	den := new(big.Rat).Add(
		new(big.Rat).Mul(new(big.Rat).SetInt64(h.A), dx),
		new(big.Rat).Mul(new(big.Rat).SetInt64(h.B), dy),
	)
	if den.Sign() == 0 {
		return Point{}, false
	}
	// t = (C - A*px - B*py) / (A*dx + B*dy)
	num := new(big.Rat).Neg(Side(h, p))
	t := num.Quo(num, den)
	return Point{
		new(big.Rat).Add(p.X, new(big.Rat).Mul(t, dx)),
		new(big.Rat).Add(p.Y, new(big.Rat).Mul(t, dy)),
	}, true
}

// Eq reports exact coordinate equality.
func Eq(p, q Point) bool {
	return p.X.Cmp(q.X) == 0 && p.Y.Cmp(q.Y) == 0
}

// SameCycle reports whether a and b are the same vertex cycle up to rotation.
func SameCycle(a, b []Point) bool {
	if len(a) != len(b) {
		return false
	}
	for s := range b {
		ok := true
		for i := range a {
			if !Eq(a[i], b[(s+i)%len(b)]) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return len(a) == 0
}

// Feasible reports whether every vertex satisfies every half-plane.
func Feasible(vs []Point, hs []HalfPlane) bool {
	for _, p := range vs {
		for _, h := range hs {
			if !Inside(h, p) {
				return false
			}
		}
	}
	return true
}

// ConvexCCW reports whether vs is a convex counter-clockwise polygon:
// every turn is a left turn (or straight) and the signed area is positive.
func ConvexCCW(vs []Point) bool {
	n := len(vs)
	if n < 3 {
		return true
	}
	area := new(big.Rat)
	for i := range vs {
		a, b, c := vs[i], vs[(i+1)%n], vs[(i+2)%n]
		dx1, dy1 := new(big.Rat).Sub(b.X, a.X), new(big.Rat).Sub(b.Y, a.Y)
		dx2, dy2 := new(big.Rat).Sub(c.X, b.X), new(big.Rat).Sub(c.Y, b.Y)
		cross := new(big.Rat).Sub(new(big.Rat).Mul(dx1, dy2), new(big.Rat).Mul(dy1, dx2))
		if cross.Sign() < 0 {
			return false
		}
		area.Add(area, new(big.Rat).Sub(new(big.Rat).Mul(a.X, b.Y), new(big.Rat).Mul(b.X, a.Y)))
	}
	return area.Sign() > 0
}
