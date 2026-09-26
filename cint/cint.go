// Package cint computes the intersection of two convex polygons by
// sequential half-plane clipping. It depends only on cvex.
package cint

import (
	"math/big"
	"ontology/cvex"
)

// Engine performs intersections and remembers how many half-plane
// clips the last intersection actually performed. The field is
// unexported on purpose: callers (and their public API) cannot read
// it; only white-box tests in this package inspect it.
type Engine struct {
	clipCount int
}

// New returns a ready Engine.
func New() *Engine { return &Engine{} }

// clips reports the number of cvex.Clip calls made by the most recent
// Intersect (unexported, white-box tests only).
func (e *Engine) clips() int { return e.clipCount }

func bbox(p []cvex.Point) (minX, maxX, minY, maxY *big.Rat) {
	minX = new(big.Rat).Set(p[0].X)
	maxX = new(big.Rat).Set(p[0].X)
	minY = new(big.Rat).Set(p[0].Y)
	maxY = new(big.Rat).Set(p[0].Y)
	for _, q := range p[1:] {
		if q.X.Cmp(minX) < 0 {
			minX.Set(q.X)
		}
		if q.X.Cmp(maxX) > 0 {
			maxX.Set(q.X)
		}
		if q.Y.Cmp(minY) < 0 {
			minY.Set(q.Y)
		}
		if q.Y.Cmp(maxY) > 0 {
			maxY.Set(q.Y)
		}
	}
	return
}

// boxesDisjoint is a strict test: touching boxes (shared edge) are
// NOT disjoint here; the zero-area rule later turns such results
// empty, exactly as the specification demands.
func boxesDisjoint(a, b []cvex.Point) bool {
	ax0, ax1, ay0, ay1 := bbox(a)
	bx0, bx1, by0, by1 := bbox(b)
	return ax1.Cmp(bx0) < 0 || bx1.Cmp(ax0) < 0 ||
		ay1.Cmp(by0) < 0 || by1.Cmp(ay0) < 0
}

func eq(p, q cvex.Point) bool { return p.X.Cmp(q.X) == 0 && p.Y.Cmp(q.Y) == 0 }

// dedupe removes consecutive duplicate vertices introduced when a
// clipping line passes exactly through a subject vertex.
func dedupe(p []cvex.Point) []cvex.Point {
	if len(p) < 2 {
		return p
	}
	out := p[:0]
	for _, q := range p {
		if len(out) == 0 || !eq(out[len(out)-1], q) {
			out = append(out, q)
		}
	}
	if len(out) > 1 && eq(out[0], out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out
}

// area2 returns twice the signed shoelace area (exact).
func area2(p []cvex.Point) *big.Rat {
	s := new(big.Rat)
	n := len(p)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		s.Add(s, new(big.Rat).Sub(
			new(big.Rat).Mul(p[i].X, p[j].Y),
			new(big.Rat).Mul(p[i].Y, p[j].X)))
	}
	return s
}

// Intersect clips A by the left half-plane of every directed edge of
// B. It returns nil when the boxes are disjoint, fewer than 3
// vertices remain, or the result has zero area (point/line contact).
func (e *Engine) Intersect(a, b []cvex.Point) []cvex.Point {
	e.clipCount = 0
	if len(a) < 3 || len(b) < 3 || boxesDisjoint(a, b) {
		return nil // O(1) empty: zero half-plane clips performed
	}
	cur := append([]cvex.Point(nil), a...)
	for i := 0; i < len(b); i++ {
		h := cvex.HalfPlaneFromEdge(b[i], b[(i+1)%len(b)])
		cur = cvex.Clip(cur, h)
		e.clipCount++
		cur = dedupe(cur)
		if len(cur) < 3 {
			return nil
		}
	}
	if len(cur) < 3 || area2(cur).Sign() == 0 {
		return nil // zero-area contact: tangent point or shared edge
	}
	return cur
}
