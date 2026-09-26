// Package api is the public facade: api -> cint -> cvex, one way.
package api

import "errors"
import "math/big"
import "ontology/cint"
import "ontology/cvex"

const maxCoord = 10000

var ErrTooFewVertices = errors.New("api: polygon needs at least 3 vertices")
var ErrCoordinateOutOfRange = errors.New("api: coordinate outside |X|,|Y| <= 10000 or non-integer")
var ErrInvalidShape = errors.New("api: not a strictly convex counter-clockwise ring")
var ErrSelfCheck = errors.New("api: self-check failed")

type Point struct{ X, Y *big.Rat }

// IPt builds an integer-lattice Point.
func IPt(x, y int64) Point { return Point{X: big.NewRat(x, 1), Y: big.NewRat(y, 1)} }

type Polygon struct {
	verts []cvex.Point
	empty bool
}

// NewPolygon succeeds only when every check passes; a rejected input leaves no partial result.
func NewPolygon(v []Point) (*Polygon, error) {
	if len(v) < 3 {
		return nil, ErrTooFewVertices
	}
	hi, lo := big.NewRat(maxCoord, 1), big.NewRat(-maxCoord, 1)
	cv := make([]cvex.Point, len(v))
	for i, p := range v {
		if p.X == nil || p.Y == nil || !p.X.IsInt() || !p.Y.IsInt() ||
			p.X.Cmp(lo) < 0 || p.X.Cmp(hi) > 0 || p.Y.Cmp(lo) < 0 || p.Y.Cmp(hi) > 0 {
			return nil, ErrCoordinateOutOfRange
		}
		cv[i] = cvex.Point{X: new(big.Rat).Set(p.X), Y: new(big.Rat).Set(p.Y)}
	}
	if !strictCCW(cv) {
		return nil, ErrInvalidShape
	}
	return &Polygon{verts: cv}, nil
}

// strictCCW certifies convexity/CCW/no-duplicate/no-self-intersection together.
func strictCCW(p []cvex.Point) bool {
	n := len(p)
	for i := range p {
		a, b := p[i], p[(i+1)%n]
		for k := 2; k < n; k++ {
			if s := cvex.Orient(a, b, p[(i+k)%n]).Sign(); s < 0 || (k == 2 && s == 0) {
				return false
			}
		}
	}
	return true
}
func (p *Polygon) Intersect(o *Polygon) (*Polygon, error) {
	if p == nil || o == nil {
		return nil, ErrInvalidShape
	}
	if p.empty || o.empty {
		return &Polygon{empty: true}, nil
	}
	r := cint.New().Intersect(p.verts, o.verts)
	return &Polygon{verts: r, empty: r == nil}, nil // stored only after completion
}

// Vertices returns a fresh copy of the CCW list (nil when empty).
func (p *Polygon) Vertices() []Point {
	if p == nil || p.empty {
		return nil
	}
	out := make([]Point, len(p.verts))
	for i, v := range p.verts {
		out[i] = Point{X: new(big.Rat).Set(v.X), Y: new(big.Rat).Set(v.Y)}
	}
	return out
}
func (p *Polygon) Empty() bool { return p == nil || p.empty }

func leftAll(q []cvex.Point, w cvex.Point, strict bool) bool {
	for i := range q {
		if s := cvex.Orient(q[i], q[(i+1)%len(q)], w).Sign(); s < 0 || (strict && s == 0) {
			return false
		}
	}
	return true
}

// naiveClip restates the specified clip order; nil when <3 verts or zero area.
func naiveClip(a, b []cvex.Point) []cvex.Point {
	cur := append([]cvex.Point(nil), a...)
	for i := range b {
		cur = cvex.Clip(cur, cvex.HalfPlaneFromEdge(b[i], b[(i+1)%len(b)]))
	}
	s := new(big.Rat)
	for i := range cur {
		j := (i + 1) % len(cur)
		s.Add(s, new(big.Rat).Sub(new(big.Rat).Mul(cur[i].X, cur[j].Y), new(big.Rat).Mul(cur[i].Y, cur[j].X)))
	}
	if len(cur) < 3 || s.Sign() == 0 {
		return nil
	}
	return cur
}

// SelfCheck verifies the four invariants on the five built-in pairs.
func (p Polygon) SelfCheck() error {
	Q := func(x0, y0, x1, y1 int64) []cvex.Point {
		return []cvex.Point{cvex.IPt(x0, y0), cvex.IPt(x1, y0), cvex.IPt(x1, y1), cvex.IPt(x0, y1)}
	}
	T := []cvex.Point{cvex.IPt(0, 0), cvex.IPt(4, 0), cvex.IPt(0, 4)}
	cs := [][2][]cvex.Point{{Q(0, 0, 4, 4), Q(2, 2, 6, 6)}, {T, Q(1, 1, 5, 5)},
		{Q(0, 0, 4, 2), Q(2, 0, 6, 4)}, {Q(0, 0, 2, 2), Q(3, 3, 5, 5)},
		{Q(0, 0, 4, 4), Q(4, 0, 8, 4)}}
	for _, c := range cs {
		r, err := (&Polygon{verts: c[0]}).Intersect(&Polygon{verts: c[1]})
		nv := naiveClip(c[0], c[1])
		if err != nil || r.Empty() != (nv == nil) || len(r.verts) != len(nv) {
			return ErrSelfCheck
		}
		if nv == nil {
			continue
		}
		for i := range nv { // invariants 1 (equal naive) and 2 (inside A and B)
			if r.verts[i].X.Cmp(nv[i].X) != 0 || r.verts[i].Y.Cmp(nv[i].Y) != 0 ||
				!leftAll(c[0], nv[i], false) || !leftAll(c[1], nv[i], false) {
				return ErrSelfCheck
			}
		}
		x, y := new(big.Rat), new(big.Rat) // invariant 3: vertex centroid
		for _, v := range nv {
			x.Add(x, v.X)
			y.Add(y, v.Y)
		}
		d := big.NewRat(int64(len(nv)), 1)
		z := cvex.Point{X: x.Quo(x, d), Y: y.Quo(y, d)}
		if !leftAll(nv, z, true) || !leftAll(c[0], z, true) || !leftAll(c[1], z, true) {
			return ErrSelfCheck
		}
	}
	return nil
}
