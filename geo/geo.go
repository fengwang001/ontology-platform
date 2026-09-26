// Package geo holds exact integer geometric predicates.
package geo

import "math/big"

// Point is a 2D point with integer coordinates.
type Point struct{ X, Y int }

// Orient2D returns >0 if a,b,c are counter-clockwise, 0 collinear, <0 clockwise.
// Exact: evaluated with arbitrary-precision integers.
func Orient2D(a, b, c Point) int {
	bx, by := int64(b.X-a.X), int64(b.Y-a.Y)
	cx, cy := int64(c.X-a.X), int64(c.Y-a.Y)
	return big.NewInt(bx*cy).Sub(
		big.NewInt(bx*cy), big.NewInt(cx*by)).Sign()
}

// InCircle returns >0 if p is strictly inside the circumcircle of (a,b,c),
// 0 if co-circular, <0 if outside, regardless of the winding of (a,b,c).
// Exact integer arithmetic; float64 is never used.
func InCircle(a, b, c, p Point) int {
	orient := Orient2D(a, b, c)
	if orient == 0 {
		return 0
	}
	// Determinant after translating a to the origin (rows b-a, c-a, p-a):
	//   |x1 y1 s1|
	//   |x2 y2 s2| ,  si = xi*xi + yi*yi
	//   |x3 y3 s3|
	// Laplace expansion along the first row; every product is big.Int.
	x1, y1 := big.NewInt(int64(b.X-a.X)), big.NewInt(int64(b.Y-a.Y))
	x2, y2 := big.NewInt(int64(c.X-a.X)), big.NewInt(int64(c.Y-a.Y))
	x3, y3 := big.NewInt(int64(p.X-a.X)), big.NewInt(int64(p.Y-a.Y))
	s1 := big.NewInt(qdist(b, a))
	s2 := big.NewInt(qdist(c, a))
	s3 := big.NewInt(qdist(p, a))

	var t, u, det big.Int
	t.Mul(y2, s3)
	u.Mul(s2, y3)
	det.Mul(x1, t.Sub(&t, &u))

	t.Mul(x2, s3)
	u.Mul(s2, x3)
	det.Sub(&det, t.Mul(y1, t.Sub(&t, &u)))

	t.Mul(x2, y3)
	u.Mul(y2, x3)
	det.Add(&det, t.Mul(s1, t.Sub(&t, &u)))

	// The translated 3x3 is the (0,3)-cofactor of the standard 4x4 incircle
	// determinant, whose cofactor sign is -1; so "inside" corresponds to the
	// 3x3 being negative for a CCW triangle.
	return -det.Sign() * orient
}

// qdist returns |u-v|^2 (fits in int64 for |coords| <= 10000).
func qdist(u, v Point) int64 {
	dx, dy := int64(u.X-v.X), int64(u.Y-v.Y)
	return dx*dx + dy*dy
}

// PointInTriangle reports whether p is strictly inside triangle (a,b,c).
// Boundary points (edge or vertex) report false.
func PointInTriangle(p, a, b, c Point) bool {
	o := Orient2D(a, b, c)
	if o == 0 {
		return false
	}
	o1 := Orient2D(a, b, p)
	o2 := Orient2D(b, c, p)
	o3 := Orient2D(c, a, p)
	if o1 == 0 || o2 == 0 || o3 == 0 {
		return false
	}
	if o > 0 {
		return o1 > 0 && o2 > 0 && o3 > 0
	}
	return o1 < 0 && o2 < 0 && o3 < 0
}
