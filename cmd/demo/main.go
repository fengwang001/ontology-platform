// Command demo exercises the incremental Delaunay triangulation packages.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/geo"
	"ontology/tri"
)

func P(x, y int) geo.Point { return geo.Point{X: x, Y: y} }

func ok(name string, pass bool) {
	if pass {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func diagonals(b *tri.Mesh) map[[2]geo.Point]bool {
	es := map[[2]geo.Point]int{}
	for _, t := range b.Triangles() {
		for k := 0; k < 3; k++ {
			a, c := t[(k+1)%3], t[(k+2)%3]
			if c.X < a.X || c.X == a.X && c.Y < a.Y {
				a, c = c, a
			}
			es[[2]geo.Point{a, c}]++
		}
	}
	out := map[[2]geo.Point]bool{}
	for e, n := range es {
		out[e] = n == 2
	}
	return out
}

func build(pts []geo.Point) *tri.Mesh {
	b := tri.New()
	for _, p := range pts {
		_ = b.Insert(p) // a collinear third point is refused by design
	}
	return b
}

// changedFaces inserts one probe into a uniform grid and counts triangles that
// disappear: an observable proxy for local cavity work (the predicate counter
// itself is unexported and never exposed).
func changedFaces(side int) int {
	pts := []geo.Point{}
	for x := 0; x < side; x++ {
		for y := 0; y < side; y++ {
			pts = append(pts, P(x*99, y*99))
		}
	}
	seed := uint64(12345) // deterministic shuffle: random insertion order
	for i := len(pts) - 1; i > 0; i-- {
		seed = seed*6364136223846793005 + 1442695040888963407
		j := int(seed>>33) % (i + 1)
		pts[i], pts[j] = pts[j], pts[i]
	}
	b := build(pts)
	before := map[[3]geo.Point]bool{}
	for _, t := range b.Triangles() {
		before[t] = true
	}
	if err := b.Insert(P(50, 50)); err != nil {
		return -1
	}
	after := map[[3]geo.Point]bool{}
	for _, t := range b.Triangles() {
		after[t] = true
	}
	n := 0
	for t := range before {
		if !after[t] {
			n++
		}
	}
	return n
}

func main() {
	pts := []geo.Point{P(0, 0), P(4, 0), P(0, 4), P(4, 4), P(3, 3), P(2, 1)}
	st := []*tri.Mesh{}
	for i := range pts {
		st = append(st, build(pts[:i+1]))
	}
	d4, d5, d6 := diagonals(st[3]), diagonals(st[4]), diagonals(st[5])
	e23, e14 := [2]geo.Point{P(0, 4), P(4, 0)}, [2]geo.Point{P(0, 0), P(4, 4)}
	e15, e36 := [2]geo.Point{P(0, 0), P(3, 3)}, [2]geo.Point{P(0, 4), P(2, 1)}
	ok("steps1-3 triangle counts 0,0,1", len(st[0].Triangles()) == 0 && len(st[1].Triangles()) == 0 && len(st[2].Triangles()) == 1)
	ok("step4 cocircular: 2 tris, diagonal kept=P2P3 not P1P4",
		len(st[3].Triangles()) == 2 && d4[e23] && !d4[e14])
	ok("step5 cavity flip: 4 tris, P2P3 gone and P1P5 appears",
		len(st[4].Triangles()) == 4 && !d5[e23] && d5[e15])
	ok("step6 cascade flip: 6 tris, illegal P1P5 gone, P3P6 appears",
		len(st[5].Triangles()) == 6 && !d6[e15] && d6[e36])

	s, _ := api.New()
	for _, p := range pts {
		if err := s.Insert(p.X, p.Y); err != nil {
			fmt.Println("FAIL", err)
			return
		}
	}
	ok("brute-force agreement + each interior edge shared by exactly 2", s.SelfCheck() == nil)

	c0 := s.Triangles()
	c, _ := api.New()
	_ = c.Insert(0, 0)
	_ = c.Insert(1, 1)
	sentinel := errors.Is(s.Insert(3, 3), api.ErrDuplicatePoint) &&
		errors.Is(s.Insert(10001, 0), api.ErrOutOfBounds) &&
		errors.Is(c.Insert(2, 2), api.ErrCollinearStart)
	ok("three distinct decidable sentinel errors", sentinel)
	ok("rejected inserts leave state unchanged", eq(c0, s.Triangles()))

	c10, c50, c100 := changedFaces(10), changedFaces(50), changedFaces(100)
	ok("work per insert stays constant as m:100->2500->10000", c10 > 0 && c10 == c50 && c50 == c100)

	var wg sync.WaitGroup
	res := make([][][3]geo.Point, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i] = s.Triangles() }(g)
	}
	wg.Wait()
	identical := true
	for i := 1; i < 8; i++ {
		identical = identical && eq(res[0], res[i])
	}
	ok("8 concurrent readers get identical triangles", identical)
}

func eq(a, b [][3]geo.Point) bool {
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
