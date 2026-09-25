package check_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/id"
	"ontology/uf"
)

type graph struct{ a [][]int }

func newGraph(n int) *graph    { return &graph{a: make([][]int, n)} }
func (g *graph) link(x, y int) { g.a[x], g.a[y] = append(g.a[x], y), append(g.a[y], x) }
func (g *graph) reach(x, y int) bool {
	seen := map[int]bool{x: true}
	for q := []int{x}; len(q) > 0; q = q[1:] {
		for _, w := range g.a[q[0]] {
			if !seen[w] {
				seen[w], q = true, append(q, w)
			}
		}
	}
	return seen[y]
}

type bad struct{ p []int }

func newBad(n int) *bad {
	s := bad{p: make([]int, n)}
	for i := range s.p {
		s.p[i] = i
	}
	return &s
}
func (s *bad) find(x int) (int, int) {
	h := 0
	for x != s.p[x] {
		x, h = s.p[x], h+1
	}
	return x, h
}
func (s *bad) union(x, y int) { rx, _ := s.find(x); ry, _ := s.find(y); s.p[ry] = rx }

func TestUnionFind(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"semantics", func(t *testing.T) {
			if uf.New(0).Count() != 0 {
				t.Fatal("empty")
			}
			one := uf.New(1)
			if ok, e := one.Connected(0, 0); e != nil || !ok || one.Count() != 1 {
				t.Fatal("single")
			}
			s := uf.New(3)
			if ok, e := s.Union(0, 1); e != nil || !ok || s.Count() != 2 {
				t.Fatal("first union")
			}
			if ok, e := s.Union(1, 2); e != nil || !ok || s.Count() != 1 {
				t.Fatal("second union")
			}
			if ok, e := s.Union(2, 0); e != nil || ok {
				t.Fatal("duplicate")
			}
			if ok, e := s.Connected(0, 2); e != nil || !ok {
				t.Fatal("transitive")
			}
		}},
		{"bfs-reference", func(t *testing.T) {
			const n = 64
			s, g, r := uf.New(n), newGraph(n), rand.New(rand.NewSource(1))
			for range 200 {
				x, y := r.Intn(n), r.Intn(n)
				before := g.reach(x, y)
				got, e := s.Union(x, y)
				if e != nil || got == before {
					t.Fatal("union reference")
				}
				if got {
					g.link(x, y)
				}
			}
			for x := 0; x < n; x++ {
				for y := x; y < n; y++ {
					if got, e := s.Connected(x, y); e != nil || got != g.reach(x, y) {
						t.Fatal("BFS reference")
					}
				}
			}
		}},
		{"balance-and-hops", func(t *testing.T) {
			const n = 1 << 16
			s, b := uf.New(n), newBad(n)
			for i := 1; i < n; i++ {
				if _, e := s.Union(i, i-1); e != nil {
					t.Fatal(e)
				}
				b.union(i, i-1)
			}
			if _, e := s.Find(n - 1); e != nil || s.LastFindHops() > 16 {
				t.Fatal("balanced height")
			}
			if _, h := b.find(n - 1); h <= 1000 {
				t.Fatal("bad height")
			}
			r, q := rand.New(rand.NewSource(7)), uf.New(n)
			for range 10000 {
				if _, e := q.Union(r.Intn(n), r.Intn(n)); e != nil {
					t.Fatal(e)
				}
			}
			bound := int(2*math.Log2(n) + 2)
			for range 1000 {
				x := r.Intn(n)
				if _, e := q.Find(x); e != nil || q.LastFindHops() > bound {
					t.Fatal("hop bound")
				}
			}
		}},
		{"errors", func(t *testing.T) {
			s := uf.New(1)
			for _, f := range []func() error{
				func() error { _, e := s.Find(-1); return e },
				func() error { _, e := s.Union(0, 1); return e },
				func() error { _, e := s.Connected(1, 0); return e },
			} {
				if !errors.Is(f(), id.ErrBadIndex) {
					t.Fatal("bad index")
				}
			}
			func() {
				defer func() {
					if !errors.Is(recover().(error), uf.ErrNegativeSize) {
						t.Fatal("negative")
					}
				}()
				uf.New(-1)
			}()
			if _, e := (*uf.Set)(nil).Find(0); !errors.Is(e, uf.ErrNilSet) {
				t.Fatal("nil")
			}
		}},
		{"concurrent-readers", func(t *testing.T) {
			s := uf.New(128)
			for i := 1; i < 64; i++ {
				if _, e := s.Union(0, i); e != nil {
					t.Fatal(e)
				}
			}
			start, wg := make(chan struct{}), sync.WaitGroup{}
			for range 16 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for range 1000 {
						if ok, e := s.Connected(0, 63); e != nil || !ok || s.Count() != 65 {
							t.Error("read")
						}
					}
				}()
			}
			close(start)
			wg.Wait()
		}},
	} {
		t.Run(tc.name, tc.run)
	}
}
