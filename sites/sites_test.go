package sites

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/nbr"
)

func gridSites(m int) (*Registry, []nbr.Point) {
	r, side, pts := New(), 1, []nbr.Point{}
	for side*side < m {
		side++
	}
	for i := 0; i < m; i++ {
		p := nbr.Point{X: (i%side - side/2) * (20000 / side), Y: (i/side - side/2) * (20000 / side)}
		if _, e := r.Add(p.X, p.Y); e != nil {
			panic(e)
		}
		pts = append(pts, p)
	}
	return r, pts
}
func naiveNearest(pts []nbr.Point, qx, qy int) int {
	best, bd := 0, int64(1<<62)
	for i, p := range pts {
		if d := nbr.Dist2(p, nbr.Point{X: qx, Y: qy}); d < bd {
			bd, best = d, i
		}
	}
	return best
}
func naiveWithin(pts []nbr.Point, qx, qy, r int) (n int) {
	for _, p := range pts {
		if nbr.Inside(nbr.Dist2(p, nbr.Point{X: qx, Y: qy}), int64(r)*int64(r)) {
			n++
		}
	}
	return
}

// TestNaiveConsistency nails invariant 1 and strict Within vs the O(n) scan.
func TestNaiveConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(20260926))
	for _, m := range []int{100, 500, 1000, 5000} {
		r, pts := gridSites(m)
		for i := 0; i < 40; i++ {
			qx, qy := rng.Intn(20001)-10000, rng.Intn(20001)-10000
			rad := rng.Intn(4000)
			gN, _ := r.Nearest(qx, qy)
			gW, _ := r.Within(qx, qy, rad)
			if gN != naiveNearest(pts, qx, qy) || gW != naiveWithin(pts, qx, qy, rad) {
				t.Fatalf("m=%d mismatch (%d,%d) r=%d: N=%d W=%d", m, qx, qy, rad, gN, gW)
			}
		}
	}
}

// TestTieStable nails invariant 3: exact ties always pick the smallest index.
func TestTieStable(t *testing.T) {
	for _, c := range []struct {
		s         [][2]int
		qx, qy, w int
	}{
		{[][2]int{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}, 0, 0, 0},
		{[][2]int{{9, 9}, {0, 1}, {1, 0}, {0, -1}, {-1, 0}}, 0, 0, 1},
	} {
		r := New()
		for _, s := range c.s {
			r.Add(s[0], s[1])
		}
		if g, e := r.Nearest(c.qx, c.qy); e != nil || g != c.w {
			t.Fatalf("ties %v: got %d,%v want %d", c.s, g, e, c.w)
		}
	}
}

// TestSublinearCandidateCount nails section 4: probed stays O(1) as m grows.
func TestSublinearCandidateCount(t *testing.T) {
	for _, m := range []int{100, 400, 1000, 4000, 10000} {
		r, _ := gridSites(m)
		worst := int64(0)
		for _, q := range [][2]int{{0, 0}, {100, 100}, {-97, 53}, {1234, -6789}} {
			r.Nearest(q[0], q[1])
			worst = max(worst, r.probed.Load())
		}
		if worst > 32 {
			t.Fatalf("m=%d probed %d > 32 (linear scan?)", m, worst)
		}
	}
}

// TestRejectedOpsNoTrace nails invariant 4: distinct sentinels, state intact.
func TestRejectedOpsNoTrace(t *testing.T) {
	r := New()
	r.Add(0, 0)
	r.Add(1, 1)
	_, eDup := r.Add(0, 0)
	_, eOOB1 := r.Add(10001, 0)
	_, eOOB2 := r.Add(0, -10001)
	_, eNeg := r.Within(0, 0, -1)
	for _, c := range [][2]error{
		{eDup, ErrDuplicate}, {eOOB1, ErrOutOfBounds}, {eOOB2, ErrOutOfBounds}, {eNeg, ErrBadRadius},
	} {
		if !errors.Is(c[0], c[1]) {
			t.Errorf("got %v want %v", c[0], c[1])
		}
	}
	if errors.Is(ErrDuplicate, ErrOutOfBounds) || errors.Is(ErrDuplicate, ErrBadRadius) ||
		errors.Is(ErrOutOfBounds, ErrBadRadius) {
		t.Fatal("the three sentinel errors are not distinct")
	}
	if r.Len() != 2 {
		t.Fatalf("rejected op changed state: Len=%d", r.Len())
	}
	if n, e := r.Within(0, 0, 5); e != nil || n != 2 {
		t.Fatalf("registry unusable after rejection: %d,%v", n, e)
	}
}

// TestConcurrentReaders nails section 6: parallel readers agree field by field.
func TestConcurrentReaders(t *testing.T) {
	r, _ := gridSites(900)
	const N = 32
	var wg sync.WaitGroup
	ns, ws := make([]int, N), make([]int, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for range [25]struct{}{} {
				n, e1 := r.Nearest(777, -333)
				w, e2 := r.Within(777, -333, 1000)
				if e1 != nil || e2 != nil {
					t.Errorf("reader %d: %v %v", g, e1, e2)
					return
				}
				ns[g], ws[g] = n, w
			}
		}(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if ns[g] != ns[0] || ws[g] != ws[0] {
			t.Fatalf("reader %d diverged: (%d,%d) want (%d,%d)", g, ns[g], ws[g], ns[0], ws[0])
		}
	}
}
