package hpi

import (
	"math/big"
	"testing"

	"ontology/hp"
)

// genSeq builds n pseudo-random half-planes (LCG, coefficients in [-20,20]).
func genSeq(seed int64, n int) []hp.HalfPlane {
	s, out := seed, make([]hp.HalfPlane, 0, n)
	next := func() int { s = s*6364136223846793005 + 1442695040888963407; return int(s>>33)%41 - 20 }
	for len(out) < n {
		if a, b, c := next(), next(), next(); a != 0 || b != 0 {
			out = append(out, hp.HalfPlane{A: int64(a), B: int64(b), C: int64(c)})
		}
	}
	return out
}

var seqs = [][]hp.HalfPlane{
	{{A: -1}, {B: -1}, {A: 1, B: 1, C: 6}, {A: 1, C: 4}, {B: 1, C: 4}},
	{{A: -1}, {B: -1}, {A: 1, B: 1, C: 6}, {A: 1, C: 4}, {B: 1, C: 4}, {A: -1, C: -5}},
	genSeq(7, 30), genSeq(99, 40), genSeq(12345, 50),
}

func runSeq(seq []hp.HalfPlane) *Region {
	r := New()
	for _, h := range seq {
		r.Add(h)
	}
	return r
}

// TestFeasibility pins invariant 2: every vertex satisfies all half-planes,
// and points strictly outside the region violate at least one.
func TestFeasibility(t *testing.T) {
	for i, seq := range seqs {
		vs := runSeq(seq).Verts()
		if !hp.Feasible(vs, seq) {
			t.Fatalf("seq %d: vertex violates a half-plane", i)
		}
		if len(vs) == 0 {
			continue
		}
		lo := vs[0]
		for _, p := range vs[1:] {
			if p.X.Cmp(lo.X) < 0 {
				lo = p
			}
		}
		for k := int64(1); k <= 8; k++ { // strictly outside => violates >= 1
			x := new(big.Rat).Sub(lo.X, new(big.Rat).SetInt64(k))
			if x.Cmp(new(big.Rat).SetInt64(1-Bound)) < 0 {
				continue // stay inside the bounding square
			}
			if hp.Feasible([]hp.Point{{X: x, Y: lo.Y}}, seq) {
				t.Fatalf("seq %d: outside point violates no half-plane", i)
			}
		}
	}
}

// TestConvexCCW pins invariant 3: convex, counter-clockwise, no repeats.
func TestConvexCCW(t *testing.T) {
	for i, seq := range seqs {
		if vs := runSeq(seq).Verts(); len(vs) > 2 && !hp.ConvexCCW(vs) {
			t.Fatalf("seq %d: region not convex CCW", i)
		}
	}
}

// TestRedundantCostConstant pins the O(1) redundancy precheck: after
// shrinking to a unit square, adding m far-away redundant half-planes must
// inspect zero region edges per Add, for every scale m.
func TestRedundantCostConstant(t *testing.T) {
	square := []hp.HalfPlane{{A: -1}, {B: -1}, {A: 1, C: 1}, {B: 1, C: 1}}
	for _, m := range []int{100, 1000, 10000} {
		r := New()
		for _, h := range square {
			r.Add(h)
		}
		for i := 0; i < m; i++ {
			r.Add(hp.HalfPlane{A: 1, B: 0, C: int64(100 + i)}) // all redundant
			if r.lastChecked != 0 {
				t.Fatalf("m=%d i=%d: redundant Add inspected %d edges, want 0", m, i, r.lastChecked)
			}
		}
		if len(r.Verts()) != 4 {
			t.Fatalf("m=%d: redundant Adds changed the region", m)
		}
	}
}

// TestClipCountsEdges pins that a real clip inspects every region edge.
func TestClipCountsEdges(t *testing.T) {
	r := New()
	r.Add(hp.HalfPlane{A: -1}) // clips the 4-edge bounding square
	if r.lastChecked != 4 {
		t.Fatalf("lastChecked = %d, want 4", r.lastChecked)
	}
}

// TestFiveSteps pins the NOTES.md five-step derivation exactly.
func TestFiveSteps(t *testing.T) {
	steps := []struct {
		h    hp.HalfPlane
		want []hp.Point
	}{
		{hp.HalfPlane{A: -1}, []hp.Point{hp.Pt(0, -Bound), hp.Pt(Bound, -Bound), hp.Pt(Bound, Bound), hp.Pt(0, Bound)}},
		{hp.HalfPlane{B: -1}, []hp.Point{hp.Pt(0, 0), hp.Pt(Bound, 0), hp.Pt(Bound, Bound), hp.Pt(0, Bound)}},
		{hp.HalfPlane{A: 1, B: 1, C: 6}, []hp.Point{hp.Pt(0, 0), hp.Pt(6, 0), hp.Pt(0, 6)}},
		{hp.HalfPlane{A: 1, C: 4}, []hp.Point{hp.Pt(0, 0), hp.Pt(4, 0), hp.Pt(4, 2), hp.Pt(0, 6)}},
		{hp.HalfPlane{B: 1, C: 4}, []hp.Point{hp.Pt(0, 0), hp.Pt(4, 0), hp.Pt(4, 2), hp.Pt(2, 4), hp.Pt(0, 4)}},
	}
	r := New()
	for i, s := range steps {
		r.Add(s.h)
		if got := r.Verts(); !hp.SameCycle(got, s.want) {
			t.Fatalf("step %d: got %v, want cycle of %v", i+1, got, s.want)
		}
	}
	// Parallel-opposite H6: x >= 5 makes the intersection empty.
	r.Add(hp.HalfPlane{A: -1, C: -5})
	if !r.Empty() || r.Verts() != nil {
		t.Fatalf("after H6: got %v, want empty", r.Verts())
	}
}
