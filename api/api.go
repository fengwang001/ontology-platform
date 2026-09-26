// Package api is the public facade for polygon boolean union.
package api

import (
	"errors"

	"ontology/poly"
	"ontology/un"
)

type Point = un.Point

var ErrTooFewVertices = errors.New("polygon: fewer than 3 vertices")
var ErrSelfIntersecting = errors.New("polygon: self-intersecting boundary")
var ErrNotCounterClockwise = errors.New("polygon: duplicate vertex or not counter-clockwise")
var ErrCoordinateOutOfRange = errors.New("polygon: coordinate outside |x|,|y|<=1e4")

const lim = 10000

type Polygon struct{ v []Point }

func pt(x, y int) Point { return Point{X: x, Y: y} }

func validate(q []Point) error {
	n := len(q)
	if n < 3 {
		return ErrTooFewVertices
	}
	for _, p := range q {
		if p.X < -lim || p.X > lim || p.Y < -lim || p.Y > lim {
			return ErrCoordinateOutOfRange
		}
	}
	if poly.Area2(q) <= 0 {
		return ErrNotCounterClockwise
	}
	for i := range q {
		if q[i] == q[(i+1)%n] {
			return ErrNotCounterClockwise
		}
	}
	for i := 0; i < n; i++ {
		a, b := q[i], q[(i+1)%n]
		for j := i + 1; j < n; j++ {
			if j == i+1 || i == 0 && j == n-1 {
				continue
			}
			c, d := q[j], q[(j+1)%n]
			bad := a == c || a == d || b == c || b == d ||
				(c != a && c != b && poly.OnSeg(a, b, c)) ||
				(d != a && d != b && poly.OnSeg(a, b, d))
			if _, ok := poly.SegIntersect(a, b, c, d); ok || bad {
				return ErrSelfIntersecting
			}
		}
	}
	return nil
}

func NewPolygon(verts []Point) (*Polygon, error) {
	q := append([]Point(nil), verts...)
	if err := validate(q); err != nil {
		return nil, err
	}
	return &Polygon{v: q}, nil
}

func (p *Polygon) Union(other *Polygon) (*Polygon, error) {
	u := un.UnionPoints(p.v, other.v)
	if err := validate(u); err != nil {
		return nil, err
	}
	return &Polygon{v: u}, nil
}

func (p *Polygon) Vertices() []Point { return append([]Point(nil), p.v...) }

func checkPair(a, b []Point) error {
	mp := func(q []Point) *Polygon { p, _ := NewPolygon(q); return p }
	u, err := mp(a).Union(mp(b))
	if err != nil {
		return err
	}
	uv := u.v
	if poly.Area2(uv) != poly.Area2(a)+poly.Area2(b)-un.IntersectionArea2(a, b) {
		return errors.New("selfcheck: area not conserved")
	}
	seen := map[Point]bool{}
	for _, w := range uv {
		seen[w] = true
	}
	for _, q := range [2][]Point{a, b} {
		for _, z := range q {
			if !seen[z] && !poly.PointInPoly(z, uv) {
				return errors.New("selfcheck: input vertex lost")
			}
		}
	}
	x0, y0, x1, y1 := uv[0].X, uv[0].Y, uv[0].X, uv[0].Y
	for _, q := range [2][]Point{a, b} {
		for _, p := range q {
			x0, x1, y0, y1 = min(x0, p.X), max(x1, p.X), min(y0, p.Y), max(y1, p.Y)
		}
	}
	for y := y0 - 1; y <= y1+1; y++ {
		for x := x0 - 1; x <= x1+1; x++ {
			z := pt(x, y)
			if poly.OnBoundary(z, a) || poly.OnBoundary(z, b) || poly.OnBoundary(z, uv) {
				continue // invariants concern strict interiors only
			}
			if poly.PointInPoly(z, uv) != (poly.PointInPoly(z, a) || poly.PointInPoly(z, b)) {
				return errors.New("selfcheck: coverage mismatch")
			}
		}
	}
	return nil
}

var pairs = [][2][]Point{
	{{pt(0, 0), pt(3, 0), pt(3, 3), pt(0, 3)}, {pt(2, 2), pt(5, 2), pt(5, 5), pt(2, 5)}},
	{{pt(0, 0), pt(6, 0), pt(6, 6), pt(0, 6)}, {pt(1, 1), pt(2, 1), pt(2, 2), pt(1, 2)}},
	{{pt(0, 0), pt(4, 0), pt(0, 4)}, {pt(2, -1), pt(6, -1), pt(2, 3)}},
	{{pt(0, 0), pt(5, 0), pt(5, 4), pt(0, 4)}, {pt(3, -2), pt(7, -2), pt(7, 2), pt(3, 2)}},
}

// SelfCheck verifies the built-in pairs against all four invariants.
func (p *Polygon) SelfCheck() error {
	for _, pr := range pairs {
		if err := checkPair(pr[0], pr[1]); err != nil {
			return err
		}
	}
	bad := []struct {
		q    []Point
		want error
	}{
		{[]Point{pt(0, 0), pt(1, 0)}, ErrTooFewVertices},
		{[]Point{pt(0, 0), pt(4, 0), pt(4, 4), pt(2, -1), pt(0, 4)}, ErrSelfIntersecting},
		{[]Point{pt(0, 0), pt(0, 0), pt(1, 0), pt(1, 1), pt(0, 1)}, ErrNotCounterClockwise},
		{[]Point{pt(0, 0), pt(0, 3), pt(3, 3), pt(3, 0)}, ErrNotCounterClockwise},
		{[]Point{pt(0, 0), pt(lim+1, 0), pt(lim+1, 1), pt(0, 1)}, ErrCoordinateOutOfRange},
	}
	for _, c := range bad {
		if _, err := NewPolygon(c.q); !errors.Is(err, c.want) {
			return errors.New("selfcheck: rejection class mismatch")
		}
	}
	_, err := NewPolygon(pairs[0][0])
	return err
}
