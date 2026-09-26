// Package geo provides exact geometric predicates on integer points.
package geo

import "math/big"

// Point is a 2D integer coordinate.
type Point struct {
	X, Y int
}

// Orient2D returns >0 if c is left of a->b (CCW), 0 if collinear, <0 if right.
// Exact: coordinate differences are <=2e4, products fit int64 with margin.
func Orient2D(a, b, c Point) int {
	d := int64(b.X-a.X)*int64(c.Y-a.Y) - int64(b.Y-a.Y)*int64(c.X-a.X)
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	}
	return 0
}

// InCircle returns >0 if p is strictly inside the circumcircle of (a,b,c),
// 0 if cocircular, <0 if outside. Exact big-integer 4x4 determinant.
func InCircle(a, b, c, p Point) int {
	at := [2]*big.Int{big.NewInt(int64(a.X - p.X)), big.NewInt(int64(a.Y - p.Y))}
	bt := [2]*big.Int{big.NewInt(int64(b.X - p.X)), big.NewInt(int64(b.Y - p.Y))}
	ct := [2]*big.Int{big.NewInt(int64(c.X - p.X)), big.NewInt(int64(c.Y - p.Y))}
	lift := func(v [2]*big.Int) *big.Int {
		x2 := new(big.Int).Mul(v[0], v[0])
		y2 := new(big.Int).Mul(v[1], v[1])
		return x2.Add(x2, y2)
	}
	al, bl, cl := lift(at), lift(bt), lift(ct)
	// det of rows (x, y, x^2+y^2) for a,b,c relative to p.
	cross := func(u, v [2]*big.Int) *big.Int {
		m1 := new(big.Int).Mul(u[0], v[1])
		m2 := new(big.Int).Mul(u[1], v[0])
		return m1.Sub(m1, m2)
	}
	term := new(big.Int).Mul(al, cross(bt, ct))
	term.Add(term, new(big.Int).Mul(bl, cross(ct, at)))
	term.Add(term, new(big.Int).Mul(cl, cross(at, bt)))
	s := term.Sign()
	if Orient2D(a, b, c) < 0 {
		s = -s
	}
	return s
}

// PointInTriangle reports whether p is strictly inside triangle (a,b,c).
func PointInTriangle(p, a, b, c Point) bool {
	o := Orient2D(a, b, c)
	if o == 0 {
		return false
	}
	return Orient2D(a, b, p)*o > 0 && Orient2D(b, c, p)*o > 0 && Orient2D(c, a, p)*o > 0
}
