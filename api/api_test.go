package api_test

import (
	"errors"
	"math/big"
	"sync"
	"testing"

	"ontology/api"
)

func P(x, y int64) api.Point { return api.IPt(x, y) }
func box(x0, y0, x1, y1 int64) []api.Point {
	return []api.Point{P(x0, y0), P(x1, y0), P(x1, y1), P(x0, y1)}
}
func ringIs(r *api.Polygon, want [][2]int64) bool {
	vs := r.Vertices()
	if len(vs) != len(want) {
		return false
	}
	for i, v := range vs {
		if v.X.Num().Int64() != want[i][0] || v.Y.Num().Int64() != want[i][1] {
			return false
		}
	}
	return true
}
func inp(a, b []api.Point) (*api.Polygon, *api.Polygon) {
	x, _ := api.NewPolygon(a)
	y, _ := api.NewPolygon(b)
	return x, y
}

func TestFivePairs(t *testing.T) {
	tri := []api.Point{P(0, 0), P(4, 0), P(0, 4)}
	cs := []struct {
		a, b []api.Point
		want [][2]int64
	}{
		{box(0, 0, 4, 4), box(2, 2, 6, 6), [][2]int64{{2, 2}, {4, 2}, {4, 4}, {2, 4}}},
		{box(0, 0, 2, 2), box(3, 3, 5, 5), nil},
		{tri, box(1, 1, 5, 5), [][2]int64{{1, 1}, {3, 1}, {1, 3}}},
		{box(0, 0, 4, 2), box(2, 0, 6, 4), [][2]int64{{2, 0}, {4, 0}, {4, 2}, {2, 2}}},
		{box(0, 0, 4, 4), box(4, 0, 8, 4), nil},
	}
	for i, c := range cs {
		a, b := inp(c.a, c.b)
		r, err := a.Intersect(b)
		if err != nil || (c.want == nil) != r.Empty() || !ringIs(r, c.want) {
			t.Fatalf("pair %d: empty=%v ring=%v err=%v", i+1, r.Empty(), r.Vertices(), err)
		}
	}
}

func TestFractionalVerticesExact(t *testing.T) {
	// Hypotenuse x+2y=4 meets the box edge x=1 at the half-integral (1,3/2).
	a, b := inp([]api.Point{P(0, 0), P(4, 0), P(0, 2)}, box(1, 1, 3, 2))
	r, err := a.Intersect(b)
	if err != nil || r.Empty() {
		t.Fatalf("intersection: %v %v", r, err)
	}
	want := [][2][2]int64{{{1, 1}, {1, 1}}, {{2, 1}, {1, 1}}, {{1, 1}, {3, 2}}}
	got := r.Vertices()
	if len(got) != len(want) {
		t.Fatalf("fractional ring = %v", got)
	}
	for k, e := range want {
		if got[k].X.Cmp(big.NewRat(e[0][0], e[0][1])) != 0 ||
			got[k].Y.Cmp(big.NewRat(e[1][0], e[1][1])) != 0 {
			t.Fatalf("vertex %d = %v", k, got[k])
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	cs := []struct {
		v    []api.Point
		want error
	}{
		{[]api.Point{P(0, 0), P(1, 0)}, api.ErrTooFewVertices},
		{box(0, 0, 4, 10001), api.ErrCoordinateOutOfRange},
		{[]api.Point{P(0, 0), P(0, 4), P(4, 4), P(4, 0)}, api.ErrInvalidShape},
		{[]api.Point{P(0, 0), P(0, 0), P(4, 0), P(0, 4)}, api.ErrInvalidShape},
		{[]api.Point{P(0, 0), P(4, 4), P(4, 0), P(0, 4)}, api.ErrInvalidShape},
		{[]api.Point{P(0, 0), P(2, 0), P(4, 0), P(4, 4), P(0, 4)}, api.ErrInvalidShape},
	}
	for i, c := range cs { // too few; out of range; clockwise/dup/bow-tie/collinear
		if p, err := api.NewPolygon(c.v); !errors.Is(err, c.want) || p != nil {
			t.Fatalf("case %d: p=%v err=%v want %v", i, p, err, c.want)
		}
	}
	if api.ErrTooFewVertices == api.ErrCoordinateOutOfRange ||
		api.ErrTooFewVertices == api.ErrInvalidShape ||
		api.ErrCoordinateOutOfRange == api.ErrInvalidShape {
		t.Fatal("sentinel errors must be distinct")
	}
}

func TestNoPartialResult(t *testing.T) {
	good, _ := api.NewPolygon(box(0, 0, 4, 4))
	before := good.Vertices()
	if bad, e := api.NewPolygon(box(0, 0, 4, 10001)); bad != nil || !errors.Is(e, api.ErrCoordinateOutOfRange) {
		t.Fatal("rejected input produced a polygon")
	}
	if len(good.Vertices()) != len(before) { // rejection left no trace
		t.Fatal("good polygon mutated by a rejected construction")
	}
	other, _ := api.NewPolygon(box(2, 2, 6, 6))
	if r, err := good.Intersect(other); err != nil ||
		!ringIs(r, [][2]int64{{2, 2}, {4, 2}, {4, 4}, {2, 4}}) {
		t.Fatalf("unusable after rejection: %v %v", r, err)
	}
}
func TestSelfCheck(t *testing.T) {
	if err := (api.Polygon{}).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentReads(t *testing.T) {
	a, b := inp(box(0, 0, 4, 4), box(2, 2, 6, 6))
	r, _ := a.Intersect(b)
	const n = 64
	var wg sync.WaitGroup
	res := make([][][2]int64, n)
	emp := make([]bool, n)
	chk := make([]error, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got := r.Vertices()
			o := make([][2]int64, len(got)) // copy under this goroutine only
			for i, v := range got {
				o[i] = [2]int64{v.X.Num().Int64(), v.Y.Num().Int64()}
			}
			res[g], emp[g], chk[g] = o, r.Empty(), r.SelfCheck()
		}(g)
	}
	wg.Wait()
	for g := 1; g < n; g++ {
		if !ringIs(r, res[g]) || emp[g] != emp[0] || chk[g] != chk[0] {
			t.Fatalf("goroutine %d saw a different result", g)
		}
	}
}
