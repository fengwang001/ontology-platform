package check

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/seg"
	"ontology/segtree"
)

func TestAgainstNaive(t *testing.T) { // 语义 1/2/3/5 + 第四节：任意交错、单点、n=1、复杂度上界
	for _, c := range []struct{ n, ops int }{{1, 60}, {2, 300}, {17, 600}, {1000, 2000}, {100000, 300}} {
		tr, ref, rng := segtree.New(c.n), NewNaive(c.n), rand.New(rand.NewSource(int64(c.n)))
		for i := 0; i < c.ops; i++ {
			l, r := rng.Intn(c.n), rng.Intn(c.n)
			if l > r {
				l, r = r, l
			}
			if rng.Intn(2) == 0 {
				d := int64(rng.Intn(21) - 10)
				if err := tr.AddRange(l, r, d); err != nil {
					t.Fatal(err)
				}
				ref.AddRange(l, r, d)
			} else if got, err := tr.SumRange(l, r); err != nil || got != ref.SumRange(l, r) {
				t.Fatalf("n=%d sum[%d,%d]=%d,%v want %d", c.n, l, r, got, err, ref.SumRange(l, r))
			}
			if v := tr.LastVisited(); float64(v) > 4*math.Log2(float64(c.n))+8 {
				t.Fatalf("n=%d op %d visited %d, 超过 4·log2(n)+8", c.n, i, v)
			}
		}
	}
}
func TestBadRange(t *testing.T) { // 语义 4：越界与 l>r 都是 ErrBadRange
	tr := segtree.New(8)
	bad := func(l, r int) {
		if err := tr.AddRange(l, r, 1); !errors.Is(err, seg.ErrBadRange) {
			t.Fatalf("add[%d,%d]: %v", l, r, err)
		}
		if _, err := tr.SumRange(l, r); !errors.Is(err, seg.ErrBadRange) {
			t.Fatalf("sum[%d,%d]: %v", l, r, err)
		}
	}
	for _, c := range []struct{ l, r int }{{-1, 3}, {0, 8}, {5, 2}, {1, 0}} {
		bad(c.l, c.r)
	}
	if _, err := tr.SumRange(0, 7); err != nil {
		t.Fatal(err)
	}
}
func TestQueryMustPushdown(t *testing.T) { // 第三节：查询不 pushdown 会读到陈旧值
	tr := segtree.New(16)
	if err := tr.AddRange(0, 15, 1); err != nil {
		t.Fatal(err)
	}
	b := newBuggy(16)
	b.add(1, 0, 15, 0, 15, 1)
	got, _ := tr.SumRange(0, 0)
	for _, c := range []struct{ got, want int64 }{{got, 1}, {b.query(1, 0, 15, 0, 0), 0}} {
		if c.got != c.want {
			t.Fatalf("SumRange(0,0)=%d, want %d", c.got, c.want)
		}
	}
}
func TestConcurrentSum(t *testing.T) { // 第五节：16 goroutine 并发只读，结果一致
	const n = 4096
	tr, ref, rng := segtree.New(n), NewNaive(n), rand.New(rand.NewSource(9))
	for i := 0; i < 500; i++ { // 写操作串行保护
		l, r := rng.Intn(n), rng.Intn(n)
		if l > r {
			l, r = r, l
		}
		d := int64(rng.Intn(7) - 3)
		_ = tr.AddRange(l, r, d)
		ref.AddRange(l, r, d)
	}
	for _, c := range []struct{ g, ops int }{{16, 300}} {
		var wg sync.WaitGroup
		for g := 0; g < c.g; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < c.ops; i++ {
					l, q := (i*97+g*13)%n, (i*31+g*7)%n
					if l > q {
						l, q = q, l
					}
					if got, err := tr.SumRange(l, q); err != nil || got != ref.SumRange(l, q) {
						t.Errorf("sum[%d,%d]=%d,%v", l, q, got, err)
					}
				}
			}(g)
		}
		wg.Wait()
	}
}
