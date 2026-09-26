package sites

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/nbr"
)

func ok(t *testing.T, cond bool, f string, a ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(f, a...)
	}
}

func TestNBR(t *testing.T) {
	cases := []struct {
		p, q nbr.Point
		d2   int64
	}{
		{nbr.Point{X: 0, Y: 0}, nbr.Point{X: 3, Y: 4}, 25},
		{nbr.Point{X: 0, Y: 2}, nbr.Point{X: 1, Y: 1}, 2},
		{nbr.Point{X: 1, Y: 0}, nbr.Point{X: 0, Y: 0}, 1},
		{nbr.Point{X: -10000, Y: -10000}, nbr.Point{X: 10000, Y: 10000}, 800000000},
	}
	for _, c := range cases {
		ok(t, nbr.Dist2(c.p, c.q) == c.d2, "Dist2(%v,%v) want d2=%d", c.p, c.q, c.d2)
		ok(t, nbr.Inside(c.d2, c.d2+1) && !nbr.Inside(c.d2, c.d2), "Inside must be strict <, d2=%d", c.d2)
	}
}

func TestWithin(t *testing.T) {
	s := New()
	for _, p := range []nbr.Point{{X: 0, Y: 0}, {X: 1, Y: 1}, {X: 4, Y: 0}, {X: 0, Y: 2}} {
		_, err := s.Add(p)
		ok(t, err == nil, "Add %v: %v", p, err)
	}
	cases := []struct{ x, y, r, want int }{
		{1, 0, 1, 0}, // r2=1: site 0 exactly on the circle -> excluded
		{1, 0, 2, 2}, // r2=4: d2 1,2 -> sites 0,1 strictly inside
		{0, 0, 2, 2}, // d2 0,2,16,4: site 3 on the circle excluded
		{0, 0, 0, 0},
	}
	for _, c := range cases {
		n, err := s.Within(nbr.Point{X: c.x, Y: c.y}, c.r)
		ok(t, err == nil && n == c.want, "Within(%d,%d,%d)=(%d,%v) want %d", c.x, c.y, c.r, n, err, c.want)
	}
	_, err := s.Within(nbr.Point{}, -1)
	ok(t, errors.Is(err, ErrNegativeRadius), "r<0 must give ErrNegativeRadius, got %v", err)
}

// naiveNearest is the O(n) reference; strict < in index order favors small idx.
func naiveNearest(pts []nbr.Point, q nbr.Point) int {
	best, bi := int64(1<<63-1), -1
	for i, p := range pts {
		if d := nbr.Dist2(p, q); d < best {
			best, bi = d, i
		}
	}
	return bi
}

func TestNaiveConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(768))
	for iter := 0; iter < 200; iter++ {
		s, used := New(), map[nbr.Point]struct{}{}
		var pts []nbr.Point
		for n := 30 + rng.Intn(70); len(pts) < n; {
			p := nbr.Point{X: rng.Intn(41) - 20, Y: rng.Intn(41) - 20}
			if _, dup := used[p]; dup {
				continue
			}
			used[p] = struct{}{}
			_, err := s.Add(p)
			ok(t, err == nil, "Add %v: %v", p, err)
			pts = append(pts, p)
		}
		for j := 0; j < 40; j++ {
			q := nbr.Point{X: rng.Intn(61) - 30, Y: rng.Intn(61) - 30}
			got, err := s.Nearest(q)
			ok(t, err == nil && got == naiveNearest(pts, q), "iter %d q=%v got %d want %d", iter, q, got, naiveNearest(pts, q))
		}
	}
}

func TestSublinearCandidates(t *testing.T) {
	const bound int64 = 16 // independent of m
	for _, k := range []int{10, 30, 50, 100} {
		m := k * k // 100, 900, 2500, 10000 uniform 100-spaced sites
		s := New()
		for i := 0; i < k; i++ {
			for j := 0; j < k; j++ {
				_, err := s.Add(nbr.Point{X: i * 100, Y: j * 100})
				ok(t, err == nil, "Add: %v", err)
			}
		}
		got, err := s.Nearest(nbr.Point{X: 50, Y: 50})
		ok(t, err == nil && got == 0, "m=%d Nearest=%d (%v) want 0", m, got, err)
		n := s.cand.Load()
		t.Logf("m=%d candidates measured=%d", m, n)
		ok(t, n <= bound, "m=%d measured %d sites, want <= %d", m, n, bound)
	}
}

func TestConcurrentNearest(t *testing.T) {
	rng := rand.New(rand.NewSource(769))
	s := New()
	for i := 0; i < 22; i++ { // 484 deterministic distinct sites
		for j := 0; j < 22; j++ {
			_, err := s.Add(nbr.Point{X: i*431 - 4700, Y: j*433 - 4700})
			ok(t, err == nil, "Add: %v", err)
		}
	}
	queries := make([]nbr.Point, 64)
	for i := range queries {
		queries[i] = nbr.Point{X: rng.Intn(20001) - 10000, Y: rng.Intn(20001) - 10000}
	}
	const nG = 16
	res := make([][]int, nG)
	var wg sync.WaitGroup
	for g := 0; g < nG; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ans := make([]int, len(queries))
			for i, q := range queries {
				idx, err := s.Nearest(q)
				if err != nil {
					t.Errorf("g%d Nearest(%v): %v", g, q, err)
					return
				}
				ans[i] = idx
			}
			res[g] = ans
		}(g)
	}
	wg.Wait()
	for g := 1; g < nG; g++ {
		ok(t, slices.Equal(res[g], res[0]), "g%d=%v want %v", g, res[g], res[0])
	}
}
