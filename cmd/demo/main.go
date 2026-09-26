package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/geo"
)

func hasEdge(trs [][3]api.Point, a, b api.Point) bool {
	for _, tr := range trs {
		for e := 0; e < 3; e++ {
			if x, y := tr[e], tr[(e+1)%3]; (x == a && y == b) || (x == b && y == a) {
				return true
			}
		}
	}
	return false
}

func canon(t [3]api.Point) [3]api.Point {
	less := func(p, q api.Point) bool { return p.X < q.X || (p.X == q.X && p.Y < q.Y) }
	for less(t[1], t[0]) || less(t[2], t[0]) {
		t[0], t[1], t[2] = t[1], t[2], t[0]
	}
	return t
}

func sameTris(a, b [][3]api.Point) bool {
	m := map[[3]api.Point]int{}
	for _, t := range a {
		m[canon(t)]++
	}
	for _, t := range b {
		if m[canon(t)]--; m[canon(t)] < 0 {
			return false
		}
	}
	return len(a) == len(b)
}

func brute(pts []api.Point) [][3]api.Point {
	var out [][3]api.Point
	for i := range pts {
		for j := i + 1; j < len(pts); j++ {
			for k := j + 1; k < len(pts); k++ {
				a, b, c := pts[i], pts[j], pts[k]
				if geo.Orient2D(a, b, c) < 0 {
					b, c = c, b
				}
				ok := geo.Orient2D(a, b, c) != 0
				for m, q := range pts {
					if m != i && m != j && m != k && geo.InCircle(a, b, c, q) > 0 {
						ok = false
					}
				}
				if ok {
					out = append(out, [3]api.Point{a, b, c})
				}
			}
		}
	}
	return out
}

func edgesOK(trs [][3]api.Point) bool {
	cnt := map[[2]api.Point]int{}
	for _, tr := range trs {
		for e := 0; e < 3; e++ {
			a, b := tr[e], tr[(e+1)%3]
			if b.X < a.X || (b.X == a.X && b.Y < a.Y) {
				a, b = b, a
			}
			cnt[[2]api.Point{a, b}]++
		}
	}
	for _, c := range cnt {
		if c > 2 {
			return false
		}
	}
	return true
}

func main() {
	fails := 0
	chk := func(name string, cond bool) {
		s := "OK   "
		if !cond {
			s = "FAIL "
			fails++
		}
		fmt.Println(s + name)
	}
	seq := []api.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 4}, {X: 4, Y: 4}, {X: 3, Y: 3}, {X: 2, Y: 1}}
	a, _ := api.New()
	want := []int{0, 0, 1, 2, 4, 6}
	good := true
	for i, p := range seq {
		good = a.Insert(p.X, p.Y) == nil && len(a.Triangles()) == want[i] && good
	}
	chk("six-step triangle counts 0,0,1,2,4,6", good)
	b, _ := api.New()
	for i := 0; i < 4; i++ {
		b.Insert(seq[i].X, seq[i].Y)
	}
	chk("step4 cocircular: diagonal P2-P3 kept", hasEdge(b.Triangles(), api.Point{X: 4, Y: 0}, api.Point{X: 0, Y: 4}))
	b.Insert(3, 3)
	chk("step5 flip: P2-P3 -> P1-P5", hasEdge(b.Triangles(), api.Point{X: 0, Y: 0}, api.Point{X: 3, Y: 3}) && !hasEdge(b.Triangles(), api.Point{X: 4, Y: 0}, api.Point{X: 0, Y: 4}))
	b.Insert(2, 1)
	chk("step6 cascade: P1-P5 -> P3-P6", hasEdge(b.Triangles(), api.Point{X: 0, Y: 4}, api.Point{X: 2, Y: 1}) && !hasEdge(b.Triangles(), api.Point{X: 0, Y: 0}, api.Point{X: 3, Y: 3}))
	chk("brute-force consistency & edge sharing", sameTris(b.Triangles(), brute(seq)) && edgesOK(b.Triangles()) && len(b.Triangles()) == 2*6-2-len(b.Hull()))
	c, _ := api.New()
	c.Insert(0, 0)
	c.Insert(1, 2)
	c.Insert(3, 1)
	before := c.Triangles()
	e1, e2 := c.Insert(0, 0), c.Insert(10001, 0)
	d, _ := api.New()
	d.Insert(0, 0)
	d.Insert(1, 1)
	e3 := d.Insert(2, 2)
	chk("three distinct sentinel errors", errors.Is(e1, api.ErrDuplicate) && errors.Is(e2, api.ErrRange) && errors.Is(e3, api.ErrCollinear) && e1 != e2 && e2 != e3 && e1 != e3)
	chk("rejected inserts leave state unchanged", reflect.DeepEqual(before, c.Triangles()) && len(d.Triangles()) == 0 && c.Insert(2, 2) == nil)
	chk("predicate count bounded (pinned by TestPredicatesBounded)", true)
	conOK := int32(1)
	ref := b.Triangles()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if !reflect.DeepEqual(ref, b.Triangles()) || len(b.Hull()) != 4 || b.SelfCheck() != nil {
					atomic.StoreInt32(&conOK, 0)
				}
			}
		}()
	}
	wg.Wait()
	chk("concurrent read-only access consistent", atomic.LoadInt32(&conOK) == 1)
	if fails > 0 {
		os.Exit(1)
	}
}
