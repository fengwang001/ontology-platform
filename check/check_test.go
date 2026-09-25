package check

import (
	"errors"
	"math/bits"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/id"
	"ontology/uf"
)

// badUF inlines the faulty policy (attach y root under x root, no balancing).
type badUF struct{ p []int }

func newBad(n int) *badUF {
	b := &badUF{p: make([]int, n)}
	for i := range b.p {
		b.p[i] = i
	}
	return b
}
func (b *badUF) find(x int) (int, int) {
	h := 0
	for b.p[x] != x {
		x, h = b.p[x], h+1
	}
	return x, h
}
func (b *badUF) union(x, y int) {
	rx, _ := b.find(x)
	ry, _ := b.find(y)
	if rx != ry {
		b.p[ry] = rx
	}
}

func TestSemantics(t *testing.T) {
	for _, c := range []struct {
		n int
		e [][2]int
		m []bool
		c int
		q [2]int
		w bool
	}{
		{0, nil, nil, 0, [2]int{}, true},
		{1, [][2]int{{0, 0}}, []bool{false}, 1, [2]int{0, 0}, true},
		{3, [][2]int{{0, 1}, {1, 2}, {0, 2}}, []bool{true, true, false}, 1, [2]int{0, 2}, true},
		{5, [][2]int{{0, 1}, {3, 4}}, []bool{true, true}, 3, [2]int{1, 3}, false},
		{4, [][2]int{{0, 1}, {2, 3}, {1, 2}}, []bool{true, true, true}, 1, [2]int{0, 3}, true},
	} {
		s, ref := uf.New(c.n), NewReference(c.n)
		for i, e := range c.e {
			m, err := s.Union(e[0], e[1])
			if err != nil || (i < len(c.m) && m != c.m[i]) {
				t.Fatalf("Union%v=%v,%v want %v", e, m, err, c.m[i])
			}
			ref.Union(e[0], e[1])
		}
		if s.Count() != c.c || ref.Count() != c.c {
			t.Fatalf("count %d ref %d want %d", s.Count(), ref.Count(), c.c)
		}
		if c.n > 0 {
			if g, err := s.Connected(c.q[0], c.q[1]); err != nil || g != c.w || g != ref.Connected(c.q[0], c.q[1]) {
				t.Fatalf("connected err %v want %v", err, c.w)
			}
		}
	}
}

func TestBFSAgreement(t *testing.T) {
	for _, n := range []int{1, 10, 500} {
		ops := n * 10
		s, ref := uf.New(n), NewReference(n)
		for range ops {
			x, y := rand.IntN(n), rand.IntN(n)
			m, err := s.Union(x, y)
			if err != nil {
				t.Fatal(err)
			}
			before := ref.Count()
			ref.Union(x, y)
			if m != (ref.Count() < before) || s.Count() != ref.Count() {
				t.Fatalf("mismatch (%d,%d)", x, y)
			}
		}
		for range 2 * n {
			x, y := rand.IntN(n), rand.IntN(n)
			if g, err := s.Connected(x, y); err != nil || g != ref.Connected(x, y) {
				t.Fatalf("(%d,%d) %v", x, y, g)
			}
		}
	}
}

func TestErrors(t *testing.T) {
	s, ts := uf.New(2), id.New(2)
	for _, c := range []struct {
		call func() error
		want error
	}{
		{func() error { _, e := s.Find(-1); return e }, uf.ErrBadIndex},
		{func() error { _, e := s.Union(0, 2); return e }, uf.ErrBadIndex},
		{func() error { _, e := s.Connected(-1, 0); return e }, uf.ErrBadIndex},
		{func() error { _, e := ts.Find(-1); return e }, id.ErrNegative},
		{func() error { _, e := ts.Union(0, 9); return e }, id.ErrTooLarge},
		{func() error { _, e := ts.Connected(0, 2); return e }, id.ErrTooLarge},
	} {
		if !errors.Is(c.call(), c.want) {
			t.Fatalf("want %v", c.want)
		}
	}
}

func TestFindPathLength(t *testing.T) {
	const n = 1 << 16
	s, b := uf.New(n), newBad(n)
	for i := 1; i < n; i++ { // (1,0),(2,1),...: y root goes under x root
		if _, err := s.Union(i, i-1); err != nil {
			t.Fatal(err)
		}
		b.union(i, i-1)
	}
	_, _ = s.Find(0)
	if s.Hops() > 16 {
		t.Fatalf("balanced hops %d > 16", s.Hops())
	}
	if _, h := b.find(0); h <= 1000 {
		t.Fatalf("naive hops %d <= 1000", h)
	}
}

func TestHopsBound(t *testing.T) {
	for _, c := range []struct{ n, ops int }{{1 << 12, 1000}, {1 << 16, 10000}, {100000, 10000}} {
		s := uf.New(c.n)
		for range c.ops {
			if _, err := s.Union(rand.IntN(c.n), rand.IntN(c.n)); err != nil {
				t.Fatal(err)
			}
		}
		bound := 2*bits.Len(uint(c.n)) + 2
		for range c.n {
			x := rand.IntN(c.n)
			if _, err := s.Find(x); err != nil || s.Hops() > bound {
				t.Fatalf("Find(%d) hops %d bound %d", x, s.Hops(), bound)
			}
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	const n, workers = 256, 16
	s := uf.New(n)
	var mu sync.RWMutex
	for i := 1; i < n; i++ { // serialized writes, then flatten every path
		if _, err := s.Union(i-1, i); err != nil {
			t.Fatal(err)
		}
	}
	for i := range n {
		if _, err := s.Find(i); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 2000 {
				x, y := rand.IntN(n), rand.IntN(n)
				mu.RLock()
				c1, e1 := s.Connected(x, y)
				c2, e2 := s.Connected(y, x)
				cnt := s.Count()
				mu.RUnlock()
				if e1 != nil || e2 != nil || c1 != c2 || cnt != 1 {
					t.Errorf("inconsistent %v %v %d", c1, c2, cnt)
					return
				}
			}
		}()
	}
	wg.Wait()
}
