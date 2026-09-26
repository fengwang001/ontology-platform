// Package cvex provides convex-polygon predicates and a single
// half-plane clipping primitive. It depends on no other package.
//
// Coordinates are exact rationals: inputs are required to be integer
// points (validated by the api package), but clipping two integer
// edges can meet at a fractional point, which is kept exact.
package cvex

import "math/big"

// Point is an exact rational point.
type Point struct {
	X, Y *big.Rat
}

// IPt builds an integer-lattice point.
func IPt(x, y int64) Point {
	return Point{X: big.NewRat(x, 1), Y: big.NewRat(y, 1)}
}

// HalfPlane is the closed left side of a directed edge A -> B:
// orient(A,B,P) >= 0.
type HalfPlane struct {
	A, B Point
}

// HalfPlaneFromEdge returns the half-plane on the left of edge a->b,
// i.e. the interior side for a counter-clockwise polygon.
func HalfPlaneFromEdge(a, b Point) HalfPlane { return HalfPlane{A: a, B: b} }

func rsub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }

// Orient returns orient(a,b,c) = cross(b-a, c-a). Positive means c is
// strictly left of a->b, zero collinear, negative right. The returned
// rat is freshly allocated.
func Orient(a, b, c Point) *big.Rat {
	dx := rsub(b.X, a.X)
	dy := rsub(b.Y, a.Y)
	cx := rsub(c.X, a.X)
	cy := rsub(c.Y, a.Y)
	return new(big.Rat).Sub(new(big.Rat).Mul(dx, cy), new(big.Rat).Mul(dy, cx))
}

// PointLeft reports p is on the closed left side of edge a->b.
func PointLeft(a, b, p Point) bool { return Orient(a, b, p).Sign() >= 0 }

func side(h HalfPlane, p Point) *big.Rat { return Orient(h.A, h.B, p) }

// crossing returns the exact intersection of segment s->e with the
// boundary line of h; the caller guarantees the endpoints differ in
// inside/outside status (one may sit exactly on the line).
func crossing(h HalfPlane, s, e Point) Point {
	ds := side(h, s)
	de := side(h, e)
	// t = -ds / (de - ds), Q = s + t*(e - s)
	t := new(big.Rat).Quo(new(big.Rat).Neg(ds), new(big.Rat).Sub(de, ds))
	qx := new(big.Rat).Add(s.X, new(big.Rat).Mul(t, rsub(e.X, s.X)))
	qy := new(big.Rat).Add(s.Y, new(big.Rat).Mul(t, rsub(e.Y, s.Y)))
	return Point{X: qx, Y: qy}
}

// Clip returns the portion of poly inside h using the
// Sutherland–Hodgman rule. The boundary (orient == 0) is kept.
// poly is treated as a closed ring in the given order.
func Clip(poly []Point, h HalfPlane) []Point {
	if len(poly) == 0 {
		return nil
	}
	out := make([]Point, 0, len(poly)+1)
	s := poly[len(poly)-1]
	sIn := side(h, s).Sign() >= 0
	for _, e := range poly {
		eIn := side(h, e).Sign() >= 0
		switch {
		case sIn && eIn:
			out = append(out, e)
		case sIn && !eIn:
			out = append(out, crossing(h, s, e))
		case !sIn && eIn:
			out = append(out, crossing(h, s, e), e)
		}
		s, sIn = e, eIn
	}
	return out
}
