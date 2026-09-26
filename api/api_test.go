package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/cgeom"
	"ontology/crel"
)

func square() []api.Point {
	return []api.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}}
}
func concave() []api.Point {
	return []api.Point{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 6}, {X: 4, Y: 6}, {X: 4, Y: 2}, {X: 2, Y: 2}, {X: 2, Y: 6}, {X: 0, Y: 6}}
}
func minD(poly []api.Point, pt api.Point) (b cgeom.Rat2) {
	for i := range poly {
		if d := cgeom.PointSegDist2(pt, poly[i], poly[(i+1)%len(poly)]); i == 0 || cgeom.CmpRat2(d, b) < 0 {
			b = d
		}
	}
	return
}
func naiveRel(poly []api.Point, x, y, r int) crel.Relation {
	pt := api.Point{X: x, Y: y}
	cmp := cgeom.CmpRat2Int(minD(poly, pt), int64(r)*int64(r))
	if cmp == 0 {
		return crel.Tangent
	}
	if cmp < 0 {
		return crel.Crossing
	}
	if cgeom.PointInPoly(pt, poly) {
		return crel.Contained
	}
	return crel.Disjoint
}
func mustNew(t *testing.T, poly []api.Point) *api.Polygon {
	p, e := api.New(poly)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func reject(t *testing.T, poly []api.Point, want error) {
	q, e := api.New(poly)
	if !errors.Is(e, want) || q != nil {
		t.Fatalf("poly=%v got (%v,%v) want %v,nil", poly, q, e, want)
	}
}
func TestPointSegDist2Clamped(t *testing.T) {
	P := func(a [2]int) cgeom.Point { return cgeom.Point{X: a[0], Y: a[1]} }
	cs := [][8]int{{2, 5, 0, 4, 4, 4, 1, 1}, {5, -2, 0, 0, 4, 0, 5, 1},
		{-3, 1, 0, 0, 4, 0, 10, 1}, {9, 1, 0, 0, 4, 0, 26, 1}, {1, 0, 0, 0, 2, 2, 1, 2}}
	for _, c := range cs {
		got := cgeom.PointSegDist2(P([2]int{c[0], c[1]}), P([2]int{c[2], c[3]}), P([2]int{c[4], c[5]}))
		if cgeom.CmpRat2(got, cgeom.Rat2{Num: int64(c[6]), Den: int64(c[7])}) != 0 {
			t.Fatalf("row %v: D=%v want %d/%d", c, got, c[6], c[7])
		}
	}
}
func TestSixCircles(t *testing.T) {
	p, sq := mustNew(t, square()), square()
	cs := [][5]int{{2, 2, 1, 4, int(crel.Contained)}, {2, 2, 3, 4, int(crel.Crossing)},
		{6, 2, 1, 4, int(crel.Disjoint)}, {5, 2, 1, 1, int(crel.Tangent)},
		{2, 5, 1, 1, int(crel.Tangent)}, {5, -2, 2, 5, int(crel.Disjoint)}}
	for _, c := range cs {
		got, e := p.Relation(c[0], c[1], c[2])
		pt := api.Point{X: c[0], Y: c[1]}
		if e != nil || got != crel.Relation(c[4]) || cgeom.CmpRat2Int(minD(sq, pt), int64(c[3])) != 0 {
			t.Fatalf("(%d,%d,r=%d)=%v,%v want %v D=%d", c[0], c[1], c[2], got, e, c[4], c[3])
		}
	}
}
func TestFourStatesCoverage(t *testing.T) {
	p, seen := mustNew(t, square()), map[crel.Relation]bool{}
	for r := 0; r <= 6; r++ {
		for _, q := range [][2]int{{2, 2}, {6, 2}, {5, 2}, {2, 5}, {-3, -3}} {
			got, e := p.Relation(q[0], q[1], r)
			if e != nil || got < crel.Crossing || got > crel.Disjoint {
				t.Fatalf("bad state %v err=%v", got, e)
			}
			seen[got] = true
		}
	}
	if len(seen) != 4 {
		t.Fatalf("not all four states reached: %v", seen)
	}
}
func TestRelationMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, poly := range [][]api.Point{square(), concave()} {
		p := mustNew(t, poly)
		for i := 0; i < 300; i++ {
			x, y, r := rng.Intn(15)-7, rng.Intn(15)-7, rng.Intn(9)
			got, e := p.Relation(x, y, r)
			if e != nil || got != naiveRel(poly, x, y, r) {
				t.Fatalf("(%d,%d,r=%d)=%v,%v want %v", x, y, r, got, e, naiveRel(poly, x, y, r))
			}
		}
	}
}
func TestRejectNoPartialResult(t *testing.T) {
	if errors.Is(api.ErrInvalidPolygon, api.ErrInvalidRadius) || errors.Is(api.ErrInvalidPolygon, api.ErrOutOfBounds) {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	bowtie := []api.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 4}, {X: 2, Y: 2}, {X: 4, Y: 4}}
	cw := []api.Point{{X: 0, Y: 0}, {X: 0, Y: 4}, {X: 4, Y: 4}, {X: 4, Y: 0}}
	dup := []api.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 4}}
	oob := []api.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 10001}}
	for _, poly := range [][]api.Point{square()[:2], dup, bowtie, cw} {
		reject(t, poly, api.ErrInvalidPolygon)
	}
	reject(t, oob, api.ErrOutOfBounds)
	p := mustNew(t, square())
	for _, c := range [][4]int{{0, 0, -1, 0}, {10001, 0, 1, 1}} {
		_, e := p.Relation(c[0], c[1], c[2])
		want := []error{api.ErrInvalidRadius, api.ErrOutOfBounds}[c[3]]
		if !errors.Is(e, want) {
			t.Fatalf("(%d,%d,r=%d) err=%v want %v", c[0], c[1], c[2], e, want)
		}
	}
	if got, _ := p.Relation(2, 2, 1); got != crel.Contained {
		t.Fatal("instance not reusable after rejected calls")
	}
	if err := p.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentRelation(t *testing.T) {
	p, res := mustNew(t, concave()), make([]crel.Relation, 64)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(len(res))
	for i := range res {
		go func(i int) { defer wg.Done(); <-start; res[i], _ = p.Relation(3, 1, 2) }(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < len(res); i++ {
		if res[i] != res[0] {
			t.Fatalf("goroutine %d got %v want %v", i, res[i], res[0])
		}
	}
}
