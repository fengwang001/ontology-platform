// Command demo prints one OK/FAIL line per requirement, non-zero exit on fail.
package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/poly"
	"ontology/un"
)

type pt = api.Point

func P(x, y int) pt { return pt{X: x, Y: y} }

var failed bool

func line(ok bool, name string) {
	tag := "OK   "
	if !ok {
		tag, failed = "FAIL ", true
	}
	fmt.Println(tag + name)
}

func simple(q []pt) bool {
	n := len(q)
	if poly.Area2(q) <= 0 {
		return false
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if j == i+1 || i == 0 && j == n-1 {
				continue
			}
			if _, ok := poly.SegIntersect(q[i], q[(i+1)%n], q[j], q[(j+1)%n]); ok {
				return false
			}
		}
	}
	return true
}

// comb is a toothed rectangle whose many teeth lie far from the overlap.
func comb(k int, left bool) []pt {
	if left {
		s := []pt{P(-9000, -1000), P(700, -1000), P(700, 1000), P(-2000, 1000)}
		x := -2000
		for i := 0; i < k; i++ {
			s = append(s, P(x, 1100), P(x-1, 1100), P(x-1, 1000), P(x-2, 1000))
			x -= 2
		}
		return append(s, P(-9000, 1000))
	}
	s := []pt{P(-700, -1200), P(2000, -1200)}
	x := 2000
	for i := 0; i < k; i++ {
		s = append(s, P(x, -1300), P(x+1, -1300), P(x+1, -1200), P(x+2, -1200))
		x += 2
	}
	return append(s, P(9000, -1200), P(9000, 1200), P(-700, 1200))
}

func main() {
	av := []pt{P(0, 0), P(3, 0), P(3, 3), P(0, 3)}
	bv := []pt{P(2, 2), P(5, 2), P(5, 5), P(2, 5)}

	steps := true
	for i, z := range av {
		if poly.PointInPoly(z, bv) != (i == 2) {
			steps = false
		}
	}
	for i, z := range bv {
		if poly.PointInPoly(z, av) != (i == 0) {
			steps = false
		}
	}
	nx := 0
	for i := range av {
		for j := range bv {
			if _, ok := poly.SegIntersect(av[i], av[(i+1)%4], bv[j], bv[(j+1)%4]); ok {
				nx++
			}
		}
	}
	line(steps && nx == 2, "ten classifications and two intersections")

	pa, _ := api.NewPolygon(av)
	pb, _ := api.NewPolygon(bv)
	u, err := pa.Union(pb)
	want := []pt{P(0, 0), P(3, 0), P(3, 2), P(5, 2), P(5, 5), P(2, 5), P(2, 3), P(0, 3)}
	line(err == nil && reflect.DeepEqual(u.Vertices(), want), "eight-vertex CCW union")

	uv := u.Vertices()
	line(poly.Area2(uv) == poly.Area2(av)+poly.Area2(bv)-un.IntersectionArea2(av, bv), "area conservation")
	line(simple(uv), "result has no self-intersection")

	bad := []struct {
		q    []pt
		want error
	}{
		{[]pt{P(0, 0), P(1, 0)}, api.ErrTooFewVertices},
		{[]pt{P(0, 0), P(4, 0), P(4, 4), P(2, -1), P(0, 4)}, api.ErrSelfIntersecting},
		{[]pt{P(0, 0), P(0, 0), P(1, 0), P(1, 1), P(0, 1)}, api.ErrNotCounterClockwise},
		{[]pt{P(0, 0), P(0, 3), P(3, 3), P(3, 0)}, api.ErrNotCounterClockwise},
		{[]pt{P(0, 0), P(10001, 0), P(10001, 1), P(0, 1)}, api.ErrCoordinateOutOfRange},
	}
	classes, partial := map[error]bool{}, false
	for _, c := range bad {
		if g, e := api.NewPolygon(c.q); e == c.want && g == nil {
			classes[c.want] = true
		} else {
			partial = true
		}
	}
	line(len(classes) == 4, "four distinct decidable errors")
	_, usable := api.NewPolygon(av)
	line(!partial && usable == nil, "no partial result; API still usable")

	// O(m) narrow-phase count is asserted in-package by the tests; here the
	// large-m union itself must succeed and stay simple.
	big := un.UnionPoints(comb(3000, true), comb(3000, false))
	line(big != nil && simple(big), "large m union (linear count asserted in tests)")

	ref := uv
	var wg sync.WaitGroup
	ok := true
	var mu sync.Mutex
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !reflect.DeepEqual(u.Vertices(), ref) || u.SelfCheck() != nil {
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	line(ok, "concurrent readers see identical results")

	if failed {
		os.Exit(1)
	}
}
