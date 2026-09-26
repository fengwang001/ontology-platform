package api

import (
	"errors"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"

	"ontology/cal"
)

func hullOf(pts []cal.Point) []cal.Point {
	sort.Slice(pts, func(i, j int) bool {
		return pts[i].X < pts[j].X || pts[i].X == pts[j].X && pts[i].Y < pts[j].Y
	})
	chain := func(rev bool) (h []cal.Point) {
		for i := range pts {
			p := pts[i]
			if rev {
				p = pts[len(pts)-1-i]
			}
			for len(h) > 1 && cal.Orient(h[len(h)-2], h[len(h)-1], p) <= 0 {
				h = h[:len(h)-1]
			}
			h = append(h, p)
		}
		return h
	}
	lower, upper := chain(false), chain(true)
	return append(lower[:len(lower)-1], upper[:len(upper)-1]...)
}

func randomConvex(r *rand.Rand, cloud int) []cal.Point {
	for {
		pts := make([]cal.Point, cloud)
		for i := range pts {
			pts[i] = cal.Point{X: int64(r.Intn(20000) - 10000), Y: int64(r.Intn(20000) - 10000)}
		}
		if h := hullOf(pts); len(h) >= 3 {
			return h
		}
	}
}

var sampleSet = func() [][]cal.Point {
	r := rand.New(rand.NewSource(774))
	out := [][]cal.Point{
		{{X: 0, Y: 0}, {X: 5, Y: 1}, {X: 6, Y: 4}, {X: 3, Y: 6}, {X: 1, Y: 5}}, {{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 4}, {X: 0, Y: 4}},
	}
	for _, c := range []int{5, 9, 20, 60, 150, 400, 2000} {
		out = append(out, randomConvex(r, c))
	}
	return out
}()

func diam(t *testing.T, poly []cal.Point) (int64, [][2]int) {
	var g Polygon
	if err := g.New(poly); err != nil {
		t.Fatalf("valid polygon rejected: %v", err)
	}
	d2, pairs, _ := g.Diameter() // New 已成功，不会再失败
	return d2, pairs
}

func TestBruteForceConsistent(t *testing.T) {
	for _, poly := range sampleSet {
		d2, _ := diam(t, poly)
		if best, _ := brute(poly); d2 != best {
			t.Fatalf("d2=%d != brute %d, poly=%v", d2, best, poly)
		}
	}
}
func TestPairsAreAntipodal(t *testing.T) {
	for _, poly := range sampleSet {
		d2, pairs := diam(t, poly)
		for _, pr := range pairs {
			if !antipodal(poly, pr[0], pr[1]) || cal.Dist2(poly[pr[0]], poly[pr[1]]) != d2 {
				t.Fatalf("pair %v not antipodal/maximal, poly=%v", pr, poly)
			}
		}
	}
}
func TestTiesComplete(t *testing.T) {
	for _, poly := range sampleSet {
		_, pairs := diam(t, poly)
		_, maxPairs := brute(poly)
		for _, pr := range pairs {
			delete(maxPairs, pr)
		}
		if len(maxPairs) > 0 {
			t.Fatalf("tied pairs missing: %v, poly=%v", maxPairs, poly)
		}
	}
}
func TestRejectNoPartial(t *testing.T) {
	cases := []struct {
		name string
		poly []cal.Point
		want error
	}{
		{"too few", []cal.Point{{X: 0, Y: 0}, {X: 1, Y: 1}}, ErrTooFewVertices},
		{"out of range", []cal.Point{{X: 0, Y: 0}, {X: 10001, Y: 0}, {X: 0, Y: 5}}, ErrOutOfRange},
		{"non-convex", []cal.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 2, Y: 2}, {X: 4, Y: 4}, {X: 0, Y: 4}}, ErrNotConvex},
		{"duplicate", []cal.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 0}, {X: 2, Y: 5}}, ErrNotConvex},
		{"clockwise", []cal.Point{{X: 0, Y: 0}, {X: 0, Y: 4}, {X: 4, Y: 4}, {X: 4, Y: 0}}, ErrNotConvex},
		{"star", []cal.Point{{X: 0, Y: 1000}, {X: -588, Y: -809}, {X: 951, Y: 309}, {X: -951, Y: 309}, {X: 588, Y: -809}}, ErrNotConvex},
	}
	for _, c := range cases {
		var g Polygon
		if err := g.New(c.poly); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
		if _, _, err := g.Diameter(); !errors.Is(err, ErrNotInitialized) { // 不留部分结果
			t.Fatalf("%s: rejected input left partial state", c.name)
		}
		if err := g.New(sampleSet[0]); err != nil { // 仍可正常使用
			t.Fatalf("%s: unusable after rejection: %v", c.name, err)
		}
		d0, p0, _ := g.Diameter()
		_ = g.New(c.poly)                                                      // 再次被拒
		if d1, p1, _ := g.Diameter(); d1 != d0 || !reflect.DeepEqual(p1, p0) { // 旧状态不被污染
			t.Fatalf("%s: rejection polluted existing state", c.name)
		}
	}
}

func TestConcurrentReadOnly(t *testing.T) {
	var g Polygon
	if err := g.New(sampleSet[0]); err != nil {
		t.Fatal(err)
	}
	d0, p0, _ := g.Diameter()
	var wg sync.WaitGroup
	fails := make([]bool, 32)
	for k := range fails {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, p, err := g.Diameter()
			fails[k] = err != nil || d != d0 || !reflect.DeepEqual(p, p0) || g.SelfCheck() != nil
		}()
	}
	wg.Wait()
	if slices.Contains(fails, true) {
		t.Fatal("goroutines got inconsistent results")
	}
}
