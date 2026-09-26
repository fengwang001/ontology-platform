// Package api is the public face of the half-plane intersection engine.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/hp"
	"ontology/hpi"
)

// Point is a region vertex (float64 view of the exact rational vertex).
type Point struct{ X, Y float64 }

// Decidable sentinel errors for rejected input; they are distinct.
var (
	ErrDegenerate = errors.New("api: degenerate half-plane (a,b)=(0,0)")
	ErrOutOfRange = errors.New("api: coefficient out of range (|a|,|b|,|c| <= 1e4)")
)

const maxCoef = 10_000

var (
	mu   sync.RWMutex
	reg  = hpi.New()
	hist []hp.HalfPlane
)

// New resets the engine to the bare bounding square.
func New() error {
	mu.Lock()
	defer mu.Unlock()
	reg = hpi.New()
	hist = nil
	return nil
}

// Add validates, then adds one half-plane. Rejected input changes nothing.
func Add(a, b, c int) error {
	if a == 0 && b == 0 {
		return ErrDegenerate
	}
	if abs(a) > maxCoef || abs(b) > maxCoef || abs(c) > maxCoef {
		return ErrOutOfRange
	}
	mu.Lock()
	defer mu.Unlock()
	h := hp.HalfPlane{A: int64(a), B: int64(b), C: int64(c)}
	reg.Add(h)
	hist = append(hist, h)
	return nil
}

// Region returns the region vertices counter-clockwise; empty when empty.
func Region() []Point {
	mu.RLock()
	defer mu.RUnlock()
	return toPoints(reg.Verts())
}

// Empty reports whether the intersection is empty.
func Empty() bool {
	mu.RLock()
	defer mu.RUnlock()
	return reg.Empty()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func toPoints(vs []hp.Point) []Point {
	out := make([]Point, 0, len(vs))
	for _, p := range vs {
		x, _ := p.X.Float64()
		y, _ := p.Y.Float64()
		out = append(out, Point{x, y})
	}
	return out
}

// SelfCheck verifies the four invariants on built-in half-plane sequences
// (known-answer naive results, feasibility, convexity, no-trace on
// rejection), plus the O(1) redundancy precheck inside hpi.
func SelfCheck() error {
	cases := []struct {
		seq  []hp.HalfPlane
		want []hp.Point // nil means empty region
	}{
		{[]hp.HalfPlane{{A: -1}, {B: -1}, {A: 1, B: 1, C: 6}, {A: 1, C: 4}, {B: 1, C: 4}},
			[]hp.Point{hp.Pt(0, 0), hp.Pt(4, 0), hp.Pt(4, 2), hp.Pt(2, 4), hp.Pt(0, 4)}},
		{[]hp.HalfPlane{{A: -1}, {B: -1}, {A: 1, B: 1, C: 6}, {A: 1, C: 4}, {B: 1, C: 4}, {A: -1, C: -5}},
			nil}, // H6 parallel-opposite to H4: empty
		{[]hp.HalfPlane{{A: -1}, {B: -1}, {A: 1, C: 1}, {B: 1, C: 1}},
			[]hp.Point{hp.Pt(0, 0), hp.Pt(1, 0), hp.Pt(1, 1), hp.Pt(0, 1)}},
	}
	for i, c := range cases {
		r := hpi.New()
		for _, h := range c.seq {
			r.Add(h)
		}
		vs := r.Verts()
		if !hp.SameCycle(vs, c.want) {
			return fmt.Errorf("api: case %d diverges from naive clip", i)
		}
		if !hp.Feasible(vs, c.seq) {
			return fmt.Errorf("api: case %d has infeasible vertex", i)
		}
		if len(vs) > 0 && !hp.ConvexCCW(vs) {
			return fmt.Errorf("api: case %d is not convex CCW", i)
		}
	}
	before := Region()
	if err := Add(0, 0, 5); !errors.Is(err, ErrDegenerate) {
		return errors.New("api: degenerate input not rejected")
	}
	if err := Add(maxCoef+1, 0, 0); !errors.Is(err, ErrOutOfRange) {
		return errors.New("api: out-of-range input not rejected")
	}
	after := Region()
	if len(before) != len(after) {
		return errors.New("api: rejected input changed state")
	}
	for i := range before {
		if before[i] != after[i] {
			return errors.New("api: rejected input changed state")
		}
	}
	return hpi.SelfCheck()
}
