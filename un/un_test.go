package un

import (
	"math/rand"
	"ontology/poly"
	"slices"
	"testing"
)

func q(x, y int) Point { return Point{X: x, Y: y} }
func comb(k int, left bool) []Point {
	if left {
		s := []Point{q(-9000, -1000), q(700, -1000), q(700, 1000), q(-2000, 1000)}
		x := -2000
		for i := 0; i < k; i++ {
			s = append(s, q(x, 1100), q(x-1, 1100), q(x-1, 1000), q(x-2, 1000))
			x -= 2
		}
		return append(s, q(-9000, 1000))
	}
	s := []Point{q(-700, -1200), q(2000, -1200)}
	x := 2000
	for i := 0; i < k; i++ {
		s = append(s, q(x, -1300), q(x+1, -1300), q(x+1, -1200), q(x+2, -1200))
		x += 2
	}
	return append(s, q(9000, -1200), q(9000, 1200), q(-700, 1200))
}
func ortho(k, hw, lo0, lo1, up0, up1 int, seed int64) []Point {
	r := rand.New(rand.NewSource(seed))
	x := func(i int) int { return -hw + 2*hw*i/(k-1) }
	lo, up := make([]int, k), make([]int, k)
	for i := 0; i < k; i++ {
		lo[i], up[i] = lo0+r.Intn(lo1-lo0), up0+r.Intn(up1-up0)
	}
	lo[k-1], up[0] = lo[k-2], up[1]
	o := []Point{}
	for i := 0; i+1 < k; i++ {
		o = append(o, q(x(i), lo[i]), q(x(i+1), lo[i]), q(x(i+1), lo[i+1]))
	}
	o = append(o, q(x(k-1), up[k-1]))
	for i := k - 1; i > 0; i-- {
		o = append(o, q(x(i), up[i]), q(x(i-1), up[i]), q(x(i-1), up[i-1]))
	}
	return slices.Compact(o)
}
func simpleCCW(s []Point) bool {
	if poly.Area2(s) <= 0 {
		return false
	}
	n := len(s)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if j == i+1 || i == 0 && j == n-1 {
				continue
			}
			if _, ok := poly.SegIntersect(s[i], s[(i+1)%n], s[j], s[(j+1)%n]); ok {
				return false
			}
		}
	}
	return true
}

var (
	boxA = []Point{q(0, 0), q(3, 0), q(3, 3), q(0, 3)}
	boxB = []Point{q(2, 2), q(5, 2), q(5, 5), q(2, 5)}
)

func TestNotesSteps(t *testing.T) {
	wantIn := []bool{false, false, true, false, true, false, false, false}
	verts := append(append([]Point{}, boxA...), boxB...)
	for i, v := range verts {
		o := boxB
		if i >= 4 {
			o = boxA
		}
		if poly.PointInPoly(v, o) != wantIn[i] {
			t.Fatalf("step %d wrong", i+1)
		}
	}
	wantX := map[Point][2]int{q(3, 2): {1, 0}, q(2, 3): {2, 3}}
	gotX := map[Point][2]int{}
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			if z, ok := poly.SegIntersect(boxA[i], boxA[(i+1)%4], boxB[j], boxB[(j+1)%4]); ok {
				gotX[z] = [2]int{i, j}
			}
		}
	}
	for p, e := range wantX {
		if gotX[p] != e {
			t.Fatalf("crossing %v=%v want %v", p, gotX[p], e)
		}
	}
	exp := []Point{q(0, 0), q(3, 0), q(3, 2), q(5, 2), q(5, 5), q(2, 5), q(2, 3), q(0, 3)}
	u := UnionPoints(boxA, boxB)
	if len(u) != len(exp) {
		t.Fatalf("union %v", u)
	}
	for i := range exp {
		if u[i] != exp[i] {
			t.Fatalf("[%d]=%v want %v", i, u[i], exp[i])
		}
	}
}
func TestLinearPredicateCount(t *testing.T) {
	// Each tooth adds ~4 edges: k maps to edge counts ~100 .. ~10000.
	for _, k := range []int{25, 100, 500, 2500} {
		a, b := comb(k, true), comb(k, false)
		m := (len(a) + len(b)) / 2
		u := UnionPoints(a, b)
		if u == nil || lastChecks.Load() > int64(12*m) { // O(m), not O(m^2)
			t.Fatalf("m=%d checks=%d nil=%v", m, lastChecks.Load(), u == nil)
		}
	}
}
func TestRandomPairs(t *testing.T) {
	for _, tc := range [][3]int{{1, 120, 40}, {2, 200, -30}, {3, -150, 60}, {4, 90, -120}} {
		seed := int64(tc[0])
		k := 6 + int(seed%8)
		a0 := ortho(k, 220, -120, -20, 20, 120, seed)
		b0 := ortho(k, 220, -120, -20, 20, 120, seed+11)
		a, b := make([]Point, len(a0)), make([]Point, len(b0))
		for i, p := range a0 {
			a[i] = q(2*p.X, 2*p.Y)
		}
		for i, p := range b0 {
			b[i] = q((2*p.X+tc[1])|1, (2*p.Y+tc[2])|1)
		}
		u := UnionPoints(a, b)
		if u == nil || !simpleCCW(u) {
			t.Fatalf("seed=%d not simple CCW", seed)
		}
		if w := poly.Area2(a) + poly.Area2(b) - IntersectionArea2(a, b); poly.Area2(u) != w {
			t.Fatalf("seed=%d area %d want %d", seed, poly.Area2(u), w)
		}
		seen := map[Point]bool{}
		for _, p := range u {
			seen[p] = true
		}
		for _, s := range [2][]Point{a, b} {
			for _, v := range s {
				if !seen[v] && !poly.PointInPoly(v, u) && !poly.OnBoundary(v, u) {
					t.Fatalf("seed=%d lost %v", seed, v)
				}
			}
		}
	}
}
