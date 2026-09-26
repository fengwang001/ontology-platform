// Command demo prints one OK/FAIL line per deliverable check.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/hp"
	"ontology/hpi"
)

var failed atomic.Int32

func ok(name string, cond bool) {
	if !cond {
		failed.Add(1)
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK   " + name)
}

func addAll(seq [][3]int) {
	_ = api.New()
	for _, s := range seq {
		_ = api.Add(s[0], s[1], s[2])
	}
}

func pt(x, y float64) api.Point { return api.Point{X: x, Y: y} }

func sameCycleF(a, b []api.Point) bool {
	if len(a) != len(b) {
		return false
	}
	for s := range b {
		match := true
		for i := range a {
			if a[i] != b[(s+i)%len(b)] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return len(a) == 0
}

func main() {
	steps := []struct {
		h    hp.HalfPlane
		want []hp.Point
	}{
		{hp.HalfPlane{A: -1}, []hp.Point{hp.Pt(0, -hpi.Bound), hp.Pt(hpi.Bound, -hpi.Bound), hp.Pt(hpi.Bound, hpi.Bound), hp.Pt(0, hpi.Bound)}},
		{hp.HalfPlane{B: -1}, []hp.Point{hp.Pt(0, 0), hp.Pt(hpi.Bound, 0), hp.Pt(hpi.Bound, hpi.Bound), hp.Pt(0, hpi.Bound)}},
		{hp.HalfPlane{A: 1, B: 1, C: 6}, []hp.Point{hp.Pt(0, 0), hp.Pt(6, 0), hp.Pt(0, 6)}},
		{hp.HalfPlane{A: 1, C: 4}, []hp.Point{hp.Pt(0, 0), hp.Pt(4, 0), hp.Pt(4, 2), hp.Pt(0, 6)}},
		{hp.HalfPlane{B: 1, C: 4}, []hp.Point{hp.Pt(0, 0), hp.Pt(4, 0), hp.Pt(4, 2), hp.Pt(2, 4), hp.Pt(0, 4)}},
	}
	r := hpi.New()
	stepOK := true
	for _, s := range steps {
		r.Add(s.h)
		stepOK = stepOK && hp.SameCycle(r.Verts(), s.want) && hp.ConvexCCW(r.Verts())
	}
	ok("five-step clip vertices (H1..H5)", stepOK)

	pent := []api.Point{pt(0, 0), pt(4, 0), pt(4, 2), pt(2, 4), pt(0, 4)}
	addAll([][3]int{{-1, 0, 0}, {0, -1, 0}, {1, 1, 6}, {1, 0, 4}, {0, 1, 4}})
	ok("final pentagon", sameCycleF(api.Region(), pent))

	addAll([][3]int{{-1, 0, 0}, {0, -1, 0}, {-1, -1, -6}, {1, 0, 4}, {0, 1, 4}}) // H3 flipped
	ok("flipped H3 wrong triangle", sameCycleF(api.Region(), []api.Point{pt(2, 4), pt(4, 2), pt(4, 4)}))

	addAll([][3]int{{-1, 0, 0}, {0, -1, 0}, {1, 1, 6}, {1, 0, 4}, {0, 1, 4}, {-1, 0, -5}}) // H6: x>=5
	ok("parallel-opposite empty", api.Empty() && len(api.Region()) == 0)

	addAll([][3]int{{-1, 0, 0}, {0, -1, 0}, {1, 1, 6}}) // boundary kept: (6,0),(0,6) inside
	ok("boundary points kept", sameCycleF(api.Region(), []api.Point{pt(0, 0), pt(6, 0), pt(0, 6)}))

	ok("selfcheck: naive/feasible/convex/redundant-O(1)", api.SelfCheck() == nil)

	before := api.Region()
	e1, e2 := api.Add(0, 0, 9), api.Add(10001, 0, 0)
	ok("decidable errors, no-trace", errors.Is(e1, api.ErrDegenerate) && errors.Is(e2, api.ErrOutOfRange) &&
		e1 != e2 && sameCycleF(before, api.Region()))

	golden := api.Region()
	var wg sync.WaitGroup
	var bad atomic.Int32
	for g := 0; g < 8; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if got := api.Region(); !sameCycleF(got, golden) {
					bad.Add(1)
					return
				}
			}
		}()
	}
	wg.Wait()
	ok("concurrent read-only identical", bad.Load() == 0)

	if failed.Load() > 0 {
		os.Exit(1)
	}
}
