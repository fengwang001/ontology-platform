package check

import (
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/id"
	"ontology/uf"
)

// badUF 复现事故实现：总把 y 的根挂到 x 根下，无平衡、无路径压缩。
type badUF struct {
	p    []int
	hops int
}

func newBad(n int) *badUF {
	b := &badUF{p: make([]int, n)}
	for i := range b.p {
		b.p[i] = i
	}
	return b
}
func (b *badUF) find(x int) int {
	b.hops = 0
	for b.p[x] != x {
		x = b.p[x]
		b.hops++
	}
	return x
}
func (b *badUF) union(x, y int) {
	rx, ry := b.find(x), b.find(y)
	if rx != ry {
		b.p[ry] = rx
	}
}

func TestSemantics(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"New(0) 合法且 Count==0", func(t *testing.T) {
			d, err := uf.New(0)
			if err != nil || d.Count() != 0 {
				t.Fatalf("New(0) = %v, count %d", err, d.Count())
			}
		}},
		{"New(1) 单元素自连", func(t *testing.T) {
			d, _ := uf.New(1)
			if ok, err := d.Connected(0, 0); err != nil || !ok || d.Count() != 1 {
				t.Fatalf("self connect ok=%v err=%v count=%d", ok, err, d.Count())
			}
		}},
		{"传递性 a~b,b~c => a~c", func(t *testing.T) {
			d, _ := uf.New(3)
			if m, _ := d.Union(0, 1); !m {
				t.Fatal("first union must merge")
			}
			if m, _ := d.Union(1, 2); !m {
				t.Fatal("second union must merge")
			}
			if ok, _ := d.Connected(0, 2); !ok {
				t.Fatal("transitivity failed")
			}
			if c := d.Count(); c != 1 {
				t.Fatalf("count=%d want 1", c)
			}
			if m, _ := d.Union(0, 2); m {
				t.Fatal("same-component union must return false")
			}
		}},
		{"三类哨兵错误可 errors.Is 区分", func(t *testing.T) {
			d, _ := uf.New(1)
			calls := []func() error{
				func() error { _, e := d.Find(5); return e },
				func() error { _, e := d.Union(-1, 0); return e },
				func() error { _, e := d.Connected(0, 9); return e },
			}
			for _, f := range calls {
				if !errors.Is(f(), uf.ErrBadIndex) {
					t.Fatal("want ErrBadIndex")
				}
			}
			if _, e := uf.New(-1); !errors.Is(e, uf.ErrNegativeSize) {
				t.Fatal("New(-1) want ErrNegativeSize")
			}
			e0, _ := uf.New(0)
			if _, e := e0.Find(0); !errors.Is(e, uf.ErrEmptySet) {
				t.Fatalf("empty Find want ErrEmptySet, got %v", e)
			}
			s, _ := id.New(1)
			if _, e := s.Connected(id.ID(0), id.ID(2)); !errors.Is(e, id.ErrBadIndex) {
				t.Fatal("id package must forward ErrBadIndex")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}

func TestAgainstBFSRef(t *testing.T) {
	cases := []struct {
		name string
		ops  [][2]int
	}{
		{"链式 0-1-...-9", chainOps(10)},
		{"固定分组", [][2]int{{0, 1}, {2, 3}, {1, 3}, {4, 5}, {5, 0}}},
		{"重复与自环", [][2]int{{1, 1}, {1, 2}, {2, 1}, {3, 2}, {0, 0}}},
		{"随机序列", randomOps(50, 60, 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := 10
			if c.name == "随机序列" {
				n = 50
			}
			d, _ := uf.New(n)
			r := NewRef(n)
			for _, op := range c.ops {
				got, ge := d.Union(op[0], op[1])
				want := r.Union(op[0], op[1])
				if ge != nil || got != want || d.Count() != r.Count() {
					t.Fatalf("union %v: got %v/%d want %v/%d", op, got, d.Count(), want, r.Count())
				}
				for x := 0; x < n; x++ {
					for y := x; y < n; y++ {
						ok, _ := d.Connected(x, y)
						if ok != r.Connected(x, y) {
							t.Fatalf("Connected(%d,%d)=%v ref=%v", x, y, ok, r.Connected(x, y))
						}
					}
				}
			}
		})
	}
}

func TestChainHeight(t *testing.T) {
	const n = 1 << 16
	cases := []struct {
		name   string
		bad    bool
		minHop int
		maxHop int
	}{
		{"按秩合并树高 <= log2(n)", false, 0, 16},
		{"朴素挂链 Find 退化 > 1000", true, 1001, n},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var hops int
			if c.bad {
				b := newBad(n)
				for i := 1; i < n; i++ {
					b.union(i, i-1)
				}
				b.find(0)
				hops = b.hops
			} else {
				d, _ := uf.New(n)
				for i := 1; i < n; i++ {
					if _, e := d.Union(i, i-1); e != nil {
						t.Fatal(e)
					}
				}
				if _, e := d.Find(0); e != nil {
					t.Fatal(e)
				}
				hops = d.LastFindHops()
			}
			if hops < c.minHop || hops > c.maxHop {
				t.Fatalf("hops=%d not in [%d,%d]", hops, c.minHop, c.maxHop)
			}
		})
	}
}

func TestRandomHopsBound(t *testing.T) {
	const n, unions = 5000, 10000
	bound := int(2*math.Log2(n)) + 2
	for _, seed := range []uint64{1, 42} {
		t.Run("seed", func(t *testing.T) {
			d, _ := uf.New(n)
			rng := rand.New(rand.NewPCG(seed, seed))
			for i := 0; i < unions; i++ {
				if _, e := d.Union(rng.IntN(n), rng.IntN(n)); e != nil {
					t.Fatal(e)
				}
			}
			for i := 0; i < n; i++ {
				if _, e := d.Find(i); e != nil {
					t.Fatal(e)
				}
				if h := d.LastFindHops(); h > bound {
					t.Fatalf("Find(%d) hops=%d > bound %d", i, h, bound)
				}
			}
		})
	}
}

func TestConcurrentReaders(t *testing.T) {
	cases := []struct{ workers int }{{16}, {8}}
	for _, c := range cases {
		t.Run("readers", func(t *testing.T) {
			const n = 1000
			d, _ := uf.New(n)
			rng := rand.New(rand.NewPCG(7, 7))
			for i := 0; i < 2*n; i++ { // 写操作串行完成
				_, _ = d.Union(rng.IntN(n), rng.IntN(n))
			}
			wantConn, _ := d.Connected(0, n-1)
			wantCount := d.Count()
			var wg sync.WaitGroup
			for w := 0; w < c.workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for k := 0; k < 2000; k++ {
						if ok, _ := d.Connected(0, n-1); ok != wantConn || d.Count() != wantCount {
							t.Errorf("inconsistent read: %v %d", ok, d.Count())
							return
						}
					}
				}()
			}
			wg.Wait()
		})
	}
}

func chainOps(n int) [][2]int {
	ops := make([][2]int, 0, n)
	for i := 1; i < n; i++ {
		ops = append(ops, [2]int{i - 1, i})
	}
	return ops
}

func randomOps(n, count int, seed uint64) [][2]int {
	rng := rand.New(rand.NewPCG(seed, seed))
	ops := make([][2]int, count)
	for i := range ops {
		ops[i] = [2]int{rng.IntN(n), rng.IntN(n)}
	}
	return ops
}
