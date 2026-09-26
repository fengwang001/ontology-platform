package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/cgeom"
	"ontology/crel"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s: %s\n", status, name)
}

func square() []api.Point {
	return []api.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}}
}

// naive is the independent O(n) reference.
func naive(poly []api.Point, x, y, r int) crel.Relation {
	pt := api.Point{X: x, Y: y}
	r2 := int64(r) * int64(r)
	n := len(poly)
	best := cgeom.PointSegDist2(pt, poly[0], poly[1])
	for i := 1; i < n; i++ {
		if d := cgeom.PointSegDist2(pt, poly[i], poly[(i+1)%n]); cgeom.CmpRat2(d, best) < 0 {
			best = d
		}
	}
	switch cgeom.CmpRat2Int(best, r2) {
	case 0:
		return crel.Tangent
	case -1:
		return crel.Crossing
	}
	if cgeom.PointInPoly(pt, poly) {
		return crel.Contained
	}
	return crel.Disjoint
}

func main() {
	sq := square()
	p, err := api.New(sq)
	if err != nil {
		fmt.Println("FAIL: build square", err)
		os.Exit(1)
	}

	// (1) six circles: exact D and relation; (2) each equals naive traversal.
	type circ struct {
		x, y, r int
		d       int64
		rel     crel.Relation
	}
	cs := []circ{{2, 2, 1, 4, crel.Contained}, {2, 2, 3, 4, crel.Crossing},
		{6, 2, 1, 4, crel.Disjoint}, {5, 2, 1, 1, crel.Tangent},
		{2, 5, 1, 1, crel.Tangent}, {5, -2, 2, 5, crel.Disjoint}}
	okSix, okNaive := true, true
	for _, c := range cs {
		got, e := p.Relation(c.x, c.y, c.r)
		pt := api.Point{X: c.x, Y: c.y}
		d := cgeom.PointSegDist2(pt, sq[0], sq[1])
		for i := 1; i < 4; i++ {
			if dd := cgeom.PointSegDist2(pt, sq[i], sq[(i+1)%4]); cgeom.CmpRat2(dd, d) < 0 {
				d = dd
			}
		}
		if e != nil || got != c.rel || cgeom.CmpRat2Int(d, c.d) != 0 {
			okSix = false
		}
		if got != naive(sq, c.x, c.y, c.r) {
			okNaive = false
		}
	}
	check("six circles D and relation", okSix)
	check("relation matches naive traversal", okNaive)

	// (3) clamped projection: interior foot vs endpoint clamp vs fractional foot.
	tl, tr := cgeom.Point{X: 0, Y: 4}, cgeom.Point{X: 4, Y: 4}
	bl, br := cgeom.Point{X: 0, Y: 0}, cgeom.Point{X: 4, Y: 0}
	clamp := cgeom.CmpRat2Int(cgeom.PointSegDist2(cgeom.Point{X: 2, Y: 5}, tl, tr), 1) == 0 &&
		cgeom.CmpRat2Int(cgeom.PointSegDist2(cgeom.Point{X: 5, Y: -2}, bl, br), 5) == 0
	check("clamped projection", clamp)

	// (4) four states are mutually exclusive and collectively exhaustive.
	states := map[crel.Relation]bool{}
	for r := 0; r <= 6; r++ {
		for _, q := range [][2]int{{2, 2}, {6, 2}, {5, 2}, {2, 5}, {-3, -3}} {
			g, e := p.Relation(q[0], q[1], r)
			if e == nil {
				states[g] = true
			}
		}
	}
	check("four states exclusive and complete", len(states) == 4)

	// (5) three distinct decidable errors; (6) no partial result + reusable.
	_, ep := api.New(sq[:2])
	_, eb := api.New([]api.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 10001}})
	_, er := p.Relation(2, 2, -1)
	three := errors.Is(ep, api.ErrInvalidPolygon) && errors.Is(eb, api.ErrOutOfBounds) && errors.Is(er, api.ErrInvalidRadius)
	check("three decidable errors", three)
	if q, _ := api.New(sq[:2]); q != nil {
		three = false
	}
	still, _ := p.Relation(2, 2, 1)
	check("rejected input leaves no partial result", three && still == crel.Contained && p.SelfCheck() == nil)

	// (7) grid: edge count does not grow with m (counter never read here).
	check("edge count does not grow with m", crel.GridScalingSelfCheck() == nil)

	// (8) concurrent read-only calls return identical results.
	const n = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	res := make([]crel.Relation, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) { defer wg.Done(); <-start; res[i], _ = p.Relation(3, 3, 1) }(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		same = same && res[i] == res[0]
	}
	check("concurrent read-only results identical", same)

	if failed {
		os.Exit(1)
	}
}
