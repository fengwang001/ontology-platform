// Package hpi maintains the intersection region of half-planes:
// a bounded convex polygon (or empty), clipped one half-plane at a time.
package hpi

import (
	"errors"
	"math/big"

	"ontology/hp"
)

// Bound is the half-side of the implicit bounding square.
const Bound = 1_000_000

// Region is the intersection of the bounding square and all added
// half-planes. Vertices are counter-clockwise with no repeats.
type Region struct {
	verts []hp.Point // empty slice means empty region
	empty bool
	// axis-aligned bounding box of verts, for O(1) redundancy precheck
	minX, maxX, minY, maxY *big.Rat
	// lastChecked records how many region edges the last Add inspected.
	// Non-exported on purpose: it must not leak into any public API.
	lastChecked int
}

// New returns the region equal to the bounding square, CCW.
func New() *Region {
	b := int64(Bound)
	r := &Region{verts: []hp.Point{hp.Pt(-b, -b), hp.Pt(b, -b), hp.Pt(b, b), hp.Pt(-b, b)}}
	r.refreshBox()
	return r
}

// Empty reports whether the region is empty.
func (r *Region) Empty() bool { return r.empty }

// Verts returns a copy of the region vertices (CCW), nil when empty.
func (r *Region) Verts() []hp.Point {
	if r.empty {
		return nil
	}
	out := make([]hp.Point, len(r.verts))
	copy(out, r.verts)
	return out
}

// Add clips the region by h. A redundant h (whole region already inside)
// is detected from the bounding box in O(1) and leaves the region untouched.
func (r *Region) Add(h hp.HalfPlane) {
	r.lastChecked = 0
	if r.empty {
		return
	}
	// Redundancy precheck: max of A*x+B*y over the bounding box.
	mx := new(big.Rat).Mul(new(big.Rat).SetInt64(h.A), pick(h.A, r.minX, r.maxX))
	my := new(big.Rat).Mul(new(big.Rat).SetInt64(h.B), pick(h.B, r.minY, r.maxY))
	if mx.Add(mx, my).Cmp(new(big.Rat).SetInt64(h.C)) <= 0 {
		return // whole box (hence whole region) already inside h
	}
	r.verts = clip(r.verts, h, &r.lastChecked)
	if len(r.verts) == 0 {
		r.empty = true
		r.minX, r.maxX, r.minY, r.maxY = nil, nil, nil, nil
		return
	}
	r.refreshBox()
}

func pick(a int64, lo, hi *big.Rat) *big.Rat {
	if a > 0 {
		return hi
	}
	return lo
}

// clip keeps the part of poly inside h (Sutherland–Hodgman, boundary kept).
func clip(poly []hp.Point, h hp.HalfPlane, checked *int) []hp.Point {
	var out []hp.Point
	push := func(p hp.Point) {
		if n := len(out); n > 0 && hp.Eq(out[n-1], p) {
			return
		}
		out = append(out, p)
	}
	for i, p := range poly {
		*checked++
		q := poly[(i+1)%len(poly)]
		pin, qin := hp.Inside(h, p), hp.Inside(h, q)
		if pin {
			push(p)
		}
		if pin != qin {
			if x, ok := hp.Intersect(h, p, q); ok {
				push(x)
			}
		}
	}
	if n := len(out); n > 1 && hp.Eq(out[0], out[n-1]) {
		out = out[:n-1]
	}
	return out
}

func (r *Region) refreshBox() {
	r.minX, r.maxX = new(big.Rat).Set(r.verts[0].X), new(big.Rat).Set(r.verts[0].X)
	r.minY, r.maxY = new(big.Rat).Set(r.verts[0].Y), new(big.Rat).Set(r.verts[0].Y)
	for _, p := range r.verts[1:] {
		if p.X.Cmp(r.minX) < 0 {
			r.minX.Set(p.X)
		}
		if p.X.Cmp(r.maxX) > 0 {
			r.maxX.Set(p.X)
		}
		if p.Y.Cmp(r.minY) < 0 {
			r.minY.Set(p.Y)
		}
		if p.Y.Cmp(r.maxY) > 0 {
			r.maxY.Set(p.Y)
		}
	}
}

// SelfCheck verifies the O(1) redundancy precheck: after shrinking to a
// unit square, adding m far-away redundant half-planes must never inspect
// more than a small constant number of edges per Add, for any m.
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		r := New()
		for _, h := range []hp.HalfPlane{{A: -1}, {B: -1}, {A: 1, C: 1}, {B: 1, C: 1}} {
			r.Add(h) // unit square [0,1]^2
		}
		for i := 0; i < m; i++ {
			r.Add(hp.HalfPlane{A: 1, B: 0, C: int64(100 + i)}) // all redundant
			if r.lastChecked > 4 {
				return errors.New("hpi: redundant Add inspected region edges")
			}
		}
		if len(r.verts) != 4 {
			return errors.New("hpi: redundant Add changed the region")
		}
	}
	return nil
}
