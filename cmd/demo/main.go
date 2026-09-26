package main

import (
	"errors"
	"fmt"
	"math/big"
	"sync"

	"ontology/api"
	"ontology/cvex"
)

func line(name string, good bool) {
	if good {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
}
func box(x0, y0, x1, y1 int64) []api.Point {
	return []api.Point{api.IPt(x0, y0), api.IPt(x1, y0), api.IPt(x1, y1), api.IPt(x0, y1)}
}
func ring(r *api.Polygon) [][2]int64 {
	vs := r.Vertices()
	out := make([][2]int64, len(vs))
	for i, v := range vs {
		out[i] = [2]int64{v.X.Num().Int64(), v.Y.Num().Int64()}
	}
	return out
}
func same(a, b [][2]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func sgn(a, b, p api.Point) int {
	return new(big.Rat).Sub(
		new(big.Rat).Mul(new(big.Rat).Sub(b.X, a.X), new(big.Rat).Sub(p.Y, a.Y)),
		new(big.Rat).Mul(new(big.Rat).Sub(b.Y, a.Y), new(big.Rat).Sub(p.X, a.X))).Sign()
}
func strictIn(q []api.Point, p api.Point) bool {
	for i := range q {
		if sgn(q[i], q[(i+1)%len(q)], p) <= 0 {
			return false
		}
	}
	return true
}
func cv(ps []api.Point) []cvex.Point {
	o := make([]cvex.Point, len(ps))
	for i, p := range ps {
		o[i] = cvex.Point{X: p.X, Y: p.Y}
	}
	return o
}

func main() {
	tri := []api.Point{api.IPt(0, 0), api.IPt(4, 0), api.IPt(0, 4)}
	type pr struct {
		a, b []api.Point
		want [][2]int64
	}
	pairs := []pr{
		{box(0, 0, 4, 4), box(2, 2, 6, 6), [][2]int64{{2, 2}, {4, 2}, {4, 4}, {2, 4}}},
		{box(0, 0, 2, 2), box(3, 3, 5, 5), nil},
		{tri, box(1, 1, 5, 5), [][2]int64{{1, 1}, {3, 1}, {1, 3}}},
		{box(0, 0, 4, 2), box(2, 0, 6, 4), [][2]int64{{2, 0}, {4, 0}, {4, 2}, {2, 2}}},
		{box(0, 0, 4, 4), box(4, 0, 8, 4), nil},
	}
	five := true
	var rs [5]*api.Polygon
	for i, q := range pairs {
		pa, _ := api.NewPolygon(q.a)
		pb, _ := api.NewPolygon(q.b)
		r, err := pa.Intersect(pb)
		rs[i] = r
		five = five && err == nil &&
			((q.want == nil && r.Empty()) || (q.want != nil && same(ring(r), q.want)))
	}
	line("five pairs P1..P5", five)

	cur, cb := cv(pairs[0].a), cv(pairs[0].b)
	for i := range cb { // reversed edge direction = take the RIGHT side
		cur = cvex.Clip(cur, cvex.HalfPlaneFromEdge(cb[(i+1)%len(cb)], cb[i]))
	}
	line("sign reversed -> empty", len(cur) == 0)
	line("bbox early-out empty", rs[1].Empty())
	line("shared-edge zero-area empty", rs[4].Empty())
	line("equals naive clip (SelfCheck)", api.Polygon{}.SelfCheck() == nil)

	r3, _ := api.NewPolygon(tri)
	b3, _ := api.NewPolygon(box(1, 1, 5, 5))
	i3, _ := r3.Intersect(b3)
	cov := strictIn(rs[0].Vertices(), api.Point{X: big.NewRat(3, 1), Y: big.NewRat(3, 1)}) &&
		strictIn(i3.Vertices(), api.Point{X: big.NewRat(3, 2), Y: big.NewRat(3, 2)})
	line("coverage: strict interior points", cov)

	_, e1 := api.NewPolygon([]api.Point{api.IPt(0, 0), api.IPt(1, 0)})
	_, e2 := api.NewPolygon(box(0, 0, 4, 10001))
	_, e3 := api.NewPolygon([]api.Point{api.IPt(0, 0), api.IPt(0, 4), api.IPt(4, 4), api.IPt(4, 0)})
	line("three distinct sentinel errors",
		errors.Is(e1, api.ErrTooFewVertices) && errors.Is(e2, api.ErrCoordinateOutOfRange) &&
			errors.Is(e3, api.ErrInvalidShape) && e1 != e2 && e2 != e3 && e1 != e3)

	good, _ := api.NewPolygon(box(0, 0, 4, 4))
	bad, berr := api.NewPolygon(box(0, 0, 4, 10001))
	other, _ := api.NewPolygon(box(2, 2, 6, 6))
	after, aerr := good.Intersect(other)
	line("rejected input leaves no partial result", bad == nil &&
		errors.Is(berr, api.ErrCoordinateOutOfRange) && aerr == nil && same(ring(after), pairs[0].want))

	base, _ := api.NewPolygon(box(-2, -2, 2, 2))
	allEmpty := true
	for i := 0; i < 10000; i++ {
		x := int64(3 + i%5000)
		bp, _ := api.NewPolygon(box(x, 3, x+1, 4))
		rr, _ := base.Intersect(bp)
		if !rr.Empty() {
			allEmpty = false
		}
	}
	line("m=10000 disjoint, 0 clips (pinned by cint test)", allEmpty)

	ref := rs[0].Vertices()
	var wg sync.WaitGroup
	oks := make([]bool, 64)
	for g := range oks {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got := rs[0].Vertices()
			oks[g] = len(got) == len(ref)
			for i := range ref {
				oks[g] = oks[g] && got[i].X.Cmp(ref[i].X) == 0 && got[i].Y.Cmp(ref[i].Y) == 0
			}
		}(g)
	}
	wg.Wait()
	conc := true
	for _, v := range oks {
		conc = conc && v
	}
	line("concurrent Vertices identical", conc)
}
