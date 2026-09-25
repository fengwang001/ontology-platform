package check

import (
	"errors"
	"math"
	"sync"
	"testing"

	"ontology/segtree"
)

type op struct {
	add  bool
	l, r int
	d    int64
}

func run(t *testing.T, n int, ops []op) {
	st, nv := segtree.New(n), NewNaive(n)
	for i, o := range ops {
		if o.add {
			_ = st.AddRange(o.l, o.r, o.d)
			nv.Add(o.l, o.r, o.d)
		} else if got, err := st.SumRange(o.l, o.r); err != nil || got != nv.Sum(o.l, o.r) {
			t.Fatalf("op %d: got %d,%v", i, got, err)
		}
	}
}
func TestMixedOps(t *testing.T) {
	cases := []struct {
		n   int
		ops []op
	}{
		{1, []op{{add: true, l: 0, r: 0, d: 5}, {l: 0, r: 0}}},
		{10, []op{{add: true, l: 0, r: 9, d: 1}, {l: 0, r: 0}, {l: 3, r: 7}}},
		{64, []op{{add: true, l: 0, r: 63, d: 2}, {l: 10, r: 20}, {add: true, l: 5, r: 50, d: -3}, {l: 0, r: 63}, {add: true, l: 63, r: 63, d: 7}, {l: 62, r: 63}}},
		{33, []op{{add: true, l: 3, r: 30, d: 4}, {add: true, l: 10, r: 11, d: 6}, {l: 0, r: 32}, {l: 10, r: 11}}},
	}
	for _, c := range cases {
		run(t, c.n, c.ops)
	}
}
func TestBadRange(t *testing.T) {
	st := segtree.New(8)
	cases := []struct {
		l, r int
		want error
	}{{-1, 3, segtree.ErrOutOfBounds}, {0, 8, segtree.ErrOutOfBounds}, {5, 2, segtree.ErrReversed}}
	for _, c := range cases {
		err := st.AddRange(c.l, c.r, 1)
		_, err2 := st.SumRange(c.l, c.r)
		if !errors.Is(err, c.want) || !errors.Is(err, segtree.ErrBadRange) || !errors.Is(err2, c.want) {
			t.Errorf("(%d,%d): %v / %v", c.l, c.r, err, err2)
		}
	}
}
func TestQueryPushdown(t *testing.T) {
	st, bg := segtree.New(16), NewStale(16)
	_ = st.AddRange(0, 15, 1)
	bg.Add(0, 15, 1)
	if got, _ := st.SumRange(0, 0); got != 1 {
		t.Fatalf("SumRange(0,0)=%d want 1", got)
	}
	if got := bg.Sum(0, 0); got != 0 {
		t.Fatalf("stale Sum(0,0)=%d want 0", got)
	}
}
func TestVisitBound(t *testing.T) {
	const n = 100000
	st := segtree.New(n)
	limit := int64(4*math.Ceil(math.Log2(n)) + 8)
	for i := 0; i < 300; i++ {
		l := (i * 331) % n
		r := l + (i*7919)%(n-l)
		_ = st.AddRange(l, r, 1)
		a := st.LastVisited()
		_, _ = st.SumRange(l, r)
		if v := max(a, st.LastVisited()); v > limit {
			t.Fatalf("op %d visited %d > %d", i, v, limit)
		}
	}
}
func TestConcurrentReads(t *testing.T) {
	st, nv := segtree.New(1000), NewNaive(1000)
	_ = st.AddRange(0, 499, 1)
	nv.Add(0, 499, 1)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Go(func() {
			for i := 0; i < 100; i++ {
				l := (g*61 + i*7) % 1000
				r := l + (g*13+i)%(1000-l)
				if got, err := st.SumRange(l, r); err != nil || got != nv.Sum(l, r) {
					t.Errorf("SumRange(%d,%d)=%d,%v", l, r, got, err)
				}
			}
		})
	}
	wg.Wait()
}
