package tri

import (
	"errors"
	"math"
	"math/rand"
	"ontology/geo"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func mkTri(t *testing.T, seed int64, n int) (*T, []geo.Point) {
	r := rand.New(rand.NewSource(seed))
	seen := map[geo.Point]bool{}
	tr := New()
	var out []geo.Point
	for len(out) < n {
		p := geo.Point{X: r.Intn(20001) - 10000, Y: r.Intn(20001) - 10000}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if err := tr.Insert(p.X, p.Y); err != nil {
			t.Fatal(err)
		}
	}
	return tr, out
}

func TestBruteForceConsistency(t *testing.T) {
	for seed, n := range []int{10, 30, 60, 45} {
		tr, pts := mkTri(t, int64(seed), n)
		trs := tr.Triangles()
		if err := checkTris(len(pts), trs); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for _, x := range trs { // every triangle has an empty circumcircle
			for _, p := range pts {
				if p != x[0] && p != x[1] && p != x[2] && geo.InCircle(x[0], x[1], x[2], p) > 0 {
					t.Fatalf("seed %d: non-empty circumcircle", seed)
				}
			}
		}
	}
}

func TestPredicatesBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := int(math.Sqrt(float64(m))) + 1
		var pts []geo.Point
		for i := 0; i < s; i++ {
			for j := 0; j < s; j++ {
				pts = append(pts, geo.Point{X: i*20000/s - 10000, Y: j*20000/s - 10000})
			}
		}
		tr := New()
		for _, ix := range rand.New(rand.NewSource(7)).Perm(len(pts)) {
			if err := tr.Insert(pts[ix].X, pts[ix].Y); err != nil {
				t.Fatal(err)
			}
		}
		if err := tr.Insert(5, 7); err != nil {
			t.Fatal(err)
		}
		if tr.preds > 64 {
			t.Fatalf("m=%d: predicates=%d, want <= 64", m, tr.preds)
		}
	}
}

func TestErrorsDistinctAndAtomic(t *testing.T) {
	tr := New()
	for _, p := range []geo.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 4}, {X: 4, Y: 4}} {
		if err := tr.Insert(p.X, p.Y); err != nil {
			t.Fatal(err)
		}
	}
	before := tr.Triangles()
	xs := []int{0, 10001, 0}
	ys := []int{0, 0, -10001}
	ws := []error{ErrDuplicate, ErrRange, ErrRange}
	for i := range xs {
		if err := tr.Insert(xs[i], ys[i]); !errors.Is(err, ws[i]) {
			t.Fatalf("insert(%d,%d): got %v, want %v", xs[i], ys[i], err, ws[i])
		}
	}
	if ErrDuplicate == ErrCollinear || ErrDuplicate == ErrRange || ErrCollinear == ErrRange {
		t.Fatal("sentinel errors are not distinct")
	}
	g := New()
	g.Insert(0, 0)
	g.Insert(1, 1)
	if err := g.Insert(2, 2); !errors.Is(err, ErrCollinear) {
		t.Fatalf("collinear: got %v", err)
	}
	if !reflect.DeepEqual(before, tr.Triangles()) || len(g.Triangles()) != 0 {
		t.Fatal("rejected inserts changed state")
	}
	if err := tr.Insert(3, 1); err != nil {
		t.Fatal("cannot continue after rejections")
	}
}

func TestConcurrentReads(t *testing.T) {
	tr, _ := mkTri(t, 9, 200)
	ref := tr.Triangles()
	var bad atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if !reflect.DeepEqual(ref, tr.Triangles()) || len(tr.Hull()) == 0 || SelfCheck() != nil {
					bad.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatal("concurrent reads inconsistent")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
