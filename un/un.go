package un

import (
	"slices"
	"sync/atomic"

	"ontology/poly"
)

type Point = poly.Point

var lastChecks atomic.Int64

const off, cells = 10000, 20001

func exposed(u, v Point, other2 []Point) bool {
	return !poly.PointInPoly(Point{X: u.X + v.X, Y: u.Y + v.Y}, other2)
}

func splits(a, b []Point) (fa, fb map[Point]Point, nCross int) {
	m, n := len(a), len(b)
	g := max(1, m+n) // edge-count resolution keeps the broad phase linear
	w := cells/g + 1
	cid := func(p Point) int { return min(g-1, (p.X+off)/w) }
	rid := func(p Point) int { return min(g-1, (p.Y+off)/w) }
	raster := func(u, v Point, f func(int, int)) {
		for x := min(cid(u), cid(v)); x <= max(cid(u), cid(v)); x++ {
			for y := min(rid(u), rid(v)); y <= max(rid(u), rid(v)); y++ {
				f(x, y)
			}
		}
	}
	cb := map[int][]int{}
	ea, eb := make([][]Point, m), make([][]Point, n)
	for j := 0; j < n; j++ {
		eb[j] = []Point{b[j], b[(j+1)%n]}
		raster(b[j], b[(j+1)%n], func(x, y int) { cb[x*g+y] = append(cb[x*g+y], j) })
	}
	for i := 0; i < m; i++ {
		ea[i] = []Point{a[i], a[(i+1)%m]}
	}
	checks := 0
	for i := 0; i < m; i++ {
		p, q := a[i], a[(i+1)%m]
		cand := map[int]bool{}
		raster(p, q, func(x, y int) {
			for _, j := range cb[x*g+y] {
				cand[j] = true
			}
		})
		for j := range cand {
			checks++
			if z, ok := poly.SegIntersect(p, q, b[j], b[(j+1)%n]); ok {
				ea[i] = append(ea[i], z)
				eb[j] = append(eb[j], z)
				nCross++
			}
		}
	}
	lastChecks.Store(int64(checks))
	build := func(edges [][]Point, src []Point) map[Point]Point {
		fwd := map[Point]Point{}
		for i, e := range edges {
			e = poly.OrderAlong(e, src[i])
			for k := 0; k+1 < len(e); k++ {
				fwd[e[k]] = e[k+1]
			}
		}
		return fwd
	}
	return build(ea, a), build(eb, b), nCross
}
func UnionPoints(a, b []Point) []Point {
	fa, fb, nCross := splits(a, b)
	if nCross == 0 { // disjoint (excluded) or containment
		if poly.PointInPoly(a[0], b) {
			return append([]Point(nil), b...)
		}
		return append([]Point(nil), a...)
	}
	fwd := [2]map[Point]Point{fa, fb}
	o2s := [2][]Point{poly.Scaled(b, 2), poly.Scaled(a, 2)}
	nodes := [2]map[Point]bool{{}, {}}
	for p := range fa {
		nodes[0][p] = true
	}
	for p := range fb {
		nodes[1][p] = true
	}
	start, first, P0 := Point{}, Point{}, -1
find:
	for owner, q0 := range [2][]Point{a, b} {
		for _, v := range q0 {
			if c := fwd[owner][v]; exposed(v, c, o2s[owner]) {
				start, first, P0 = v, c, owner
				break find
			}
		}
	}
	out := []Point{start}
	prev, cur, P := start, first, P0
	for cur != start {
		out = append(out, cur)
		if nodes[P^1][cur] { // crossing: take the exposed, rightmost forward edge
			best, w, wP := 2, Point{}, -1
			for owner := 0; owner < 2; owner++ {
				c, ok := fwd[owner][cur]
				if ok && exposed(cur, c, o2s[owner]) && (wP < 0 || poly.Orient(prev, cur, c) < best) {
					best, w, wP = poly.Orient(prev, cur, c), c, owner
				}
			}
			if wP < 0 {
				return nil
			}
			prev, cur, P = cur, w, wP
			continue
		}
		nx, ok := fwd[P][cur]
		if !ok {
			return nil
		}
		prev, cur = cur, nx
	}
	if poly.Area2(out) < 0 {
		slices.Reverse(out)
	}
	if poly.Area2(out) == 0 {
		return nil
	}
	return out
}
func IntersectionArea2(a, b []Point) int {
	fa, fb, _ := splits(a, b)
	a2, b2 := poly.Scaled(a, 2), poly.Scaled(b, 2)
	sum := int64(0)
	seg := func(fm map[Point]Point, o2 []Point) {
		for u, v := range fm {
			if !exposed(u, v, o2) {
				sum += int64(u.X)*int64(v.Y) - int64(u.Y)*int64(v.X)
			}
		}
	}
	seg(fa, b2)
	seg(fb, a2)
	if sum < 0 {
		sum = -sum
	}
	return int(sum)
}
