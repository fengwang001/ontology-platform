package cint

import (
	"math/big"
	"math/rand"
	"testing"

	"ontology/cvex"
)

type P = cvex.Point

func rp(x, y int64) P       { return cvex.IPt(x, y) }
func cr(a, b, c P) *big.Rat { return cvex.Orient(a, b, c) }
func rect(x0, y0, x1, y1 int64) []P {
	return []P{rp(x0, y0), rp(x1, y0), rp(x1, y1), rp(x0, y1)}
}

// shRef is an independent Sutherland-Hodgman reference on big.Rat.
func shRef(poly []P, a, b P) []P {
	var out []P
	xi := func(s, e P) P {
		t := new(big.Rat).Quo(new(big.Rat).Neg(cr(a, b, s)), new(big.Rat).Sub(cr(a, b, e), cr(a, b, s)))
		return P{X: new(big.Rat).Add(s.X, new(big.Rat).Mul(t, new(big.Rat).Sub(e.X, s.X))), Y: new(big.Rat).Add(s.Y, new(big.Rat).Mul(t, new(big.Rat).Sub(e.Y, s.Y)))}
	}
	s, sin := poly[len(poly)-1], cr(a, b, poly[len(poly)-1]).Sign() >= 0
	for _, e := range poly {
		ein := cr(a, b, e).Sign() >= 0
		if sin != ein {
			out = append(out, xi(s, e))
		}
		if ein {
			out = append(out, e)
		}
		s, sin = e, ein
	}
	return out
}

// naiveRef clips A by every edge of B; nil when empty or zero area.
func naiveRef(a, b []P) []P {
	cur := append([]P(nil), a...)
	for i := range b {
		if len(cur) == 0 {
			return nil
		}
		cur = dedupe(shRef(cur, b[i], b[(i+1)%len(b)]))
	}
	if len(cur) < 3 || area2(cur).Sign() == 0 {
		return nil
	}
	return cur
}
func rngPoly(r *rand.Rand) []P {
	c := func() int64 { return int64(r.Intn(13) - 7) }
	if r.Intn(2) == 0 {
		x0, x1, y0, y1 := c(), c(), c(), c()
		if x0 == x1 {
			x1++
		}
		if y0 == y1 {
			y1++
		}
		return rect(min(x0, x1), min(y0, y1), max(x0, x1), max(y0, y1))
	}
	q := []P{rp(c(), c()), rp(c(), c()), rp(c(), c())}
	if s := cr(q[0], q[1], q[2]).Sign(); s < 0 {
		q[1], q[2] = q[2], q[1]
	} else if s == 0 {
		return rect(-1, -1, 1, 1)
	}
	return q
}
func inside(q []P, p P, strict bool) bool {
	if len(q) < 3 {
		return false
	}
	for i := range q {
		if s := cr(q[i], q[(i+1)%len(q)], p).Sign(); s < 0 || (strict && s == 0) {
			return false
		}
	}
	return true
}
func TestClipBasics(t *testing.T) {
	a, b := rp(0, 0), rp(4, 0)
	if !cvex.PointLeft(a, b, rp(2, 2)) || cvex.PointLeft(a, b, rp(2, -1)) {
		t.Fatal("PointLeft wrong")
	}
	if g := cvex.Clip(rect(0, -2, 4, 2), cvex.HalfPlaneFromEdge(a, b)); len(g) != 4 {
		t.Fatalf("clip kept %d vertices, want 4", len(g))
	}
}
func TestBBoxEarlyOutZeroClips(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e, A := New(), rect(-2, -2, 2, 2)
		for i := 0; i < m; i++ {
			x := int64(3 + i%2000)
			if r := e.Intersect(A, rect(x, 3, x+1, 4)); r != nil || e.clips() != 0 {
				t.Fatalf("m=%d i=%d: clips=%d, want empty and 0 clips", m, i, e.clips())
			}
		}
	}
}
func TestClipCountPositive(t *testing.T) {
	e := New()
	if r := e.Intersect(rect(0, 0, 4, 4), rect(2, 2, 6, 6)); r == nil || e.clips() != 4 {
		t.Fatalf("clips=%d, want 4", e.clips())
	}
}
func TestNaiveConsistencyRandom(t *testing.T) {
	r, e := rand.New(rand.NewSource(773)), New()
	for n := 0; n < 300; n++ {
		a, b := rngPoly(r), rngPoly(r)
		got, want := e.Intersect(a, b), naiveRef(a, b)
		if (got == nil) != (want == nil) || len(got) != len(want) {
			t.Fatalf("pair %d: %d verts, want %d", n, len(got), len(want))
		}
		for i := range want {
			if !eq(got[i], want[i]) {
				t.Fatalf("pair %d vertex %d mismatch", n, i)
			}
		}
	}
}
func TestCoverageLattice(t *testing.T) {
	r, e := rand.New(rand.NewSource(7737)), New()
	for n := 0; n < 100; n++ {
		a, b := rngPoly(r), rngPoly(r)
		got := e.Intersect(a, b)
		for x := int64(-8); x <= 8; x++ {
			for y := int64(-8); y <= 8; y++ {
				p := rp(x, y)
				if inside(a, p, true) && inside(b, p, true) && !inside(got, p, true) {
					t.Fatalf("pair %d: strict A&B point (%d,%d) missing", n, x, y)
				}
				if got != nil && inside(got, p, false) && (!inside(a, p, false) || !inside(b, p, false)) {
					t.Fatalf("pair %d: result point (%d,%d) escapes", n, x, y)
				}
			}
		}
		for _, v := range got {
			if !inside(a, v, false) || !inside(b, v, false) {
				t.Fatalf("pair %d: vertex %v outside an input", n, v)
			}
		}
	}
}
