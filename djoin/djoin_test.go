package djoin

import (
	"errors"
	"maps"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/rel"
)

func rw(k int64, v string, s int) rel.Row    { return rel.Row{K: k, V: v, Sign: s} }
func dl(k int64, a, b string, m int64) Delta { return Delta{K: k, A: a, B: b, Mult: m} }
func bad(t *testing.T, c bool, f string, a ...any) {
	if c {
		t.Fatalf(f, a...)
	}
}

type fz struct {
	e    *Engine
	r, s *rel.Table
	rng  *rand.Rand
	down map[tkey]int64
}

func (f *fz) rows(t *rel.Table, vs ...string) (out []rel.Row) {
	for i := 1 + f.rng.Intn(4); i > 0; i-- {
		k, v, sg := f.rng.Int63n(8)+1, vs[f.rng.Intn(len(vs))], 1
		if sp := t.Snapshot(); len(sp) > 0 && f.rng.Intn(2) == 0 {
			x := sp[f.rng.Intn(len(sp))]
			k, v, sg = x.K, x.V, -1
		}
		t.Add(k, v, int64(sg))
		out = append(out, rw(k, v, sg))
	}
	return
}
func (f *fz) feed() ([]Delta, error) {
	return f.e.Feed(f.rows(f.r, "a", "b"), f.rows(f.s, "p", "q"))
}
func same(g []Delta, w map[tkey]int64) bool { return len(g) == len(w) && maps.Equal(asMap(g), w) }
func TestViewMatchesRecompute(t *testing.T) {
	for sd := int64(0); sd < 6; sd++ {
		f := &fz{e: New(100000), r: rel.New(), s: rel.New(), rng: rand.New(rand.NewSource(sd)), down: map[tkey]int64{}}
		for b := 0; b < 50; b++ {
			_, err := f.feed()
			bad(t, err != nil || !same(f.e.View(), recompute(f.e.r, f.e.s)), "seed %d batch %d: %v", sd, b, err)
		}
	}
}
func TestDeltaMatchesNaive(t *testing.T) {
	e := New(100)
	st := []struct {
		dr, ds []rel.Row
		want   []Delta
	}{
		{[]rel.Row{rw(1, "x", 1), rw(2, "y", 1)}, []rel.Row{rw(1, "p", 1), rw(2, "q", 1)}, []Delta{dl(1, "x", "p", 1), dl(2, "y", "q", 1)}},
		{[]rel.Row{rw(1, "z", 1), rw(2, "y", -1)}, []rel.Row{rw(1, "r", 1), rw(2, "q", 1)}, []Delta{dl(1, "x", "r", 1), dl(1, "z", "p", 1), dl(1, "z", "r", 1), dl(2, "y", "q", -1)}},
		{[]rel.Row{rw(1, "x", -1)}, []rel.Row{rw(1, "r", -1)}, []Delta{dl(1, "x", "p", -1), dl(1, "x", "r", -1), dl(1, "z", "r", -1)}},
	}
	prev := map[tkey]int64{}
	for i, s := range st { // NOTES 第三节三批：精确有序差分 + 朴素全量求差双对照
		got, err := e.Feed(s.dr, s.ds)
		nw := recompute(e.r, e.s)
		bad(t, err != nil || !slices.Equal(got, s.want) || !same(got, naiveDiff(prev, nw)), "step %d: %v %v", i, got, err)
		prev = nw
	}
	f := &fz{e: New(100000), r: rel.New(), s: rel.New(), rng: rand.New(rand.NewSource(9)), down: map[tkey]int64{}}
	for b := 0; b < 100; b++ {
		old := recompute(f.e.r, f.e.s)
		d, err := f.feed()
		bad(t, err != nil || !same(d, naiveDiff(old, recompute(f.e.r, f.e.s))), "batch %d: %v %v", b, d, err)
	}
}
func TestNonNegPrefixes(t *testing.T) {
	f := &fz{e: New(100000), r: rel.New(), s: rel.New(), rng: rand.New(rand.NewSource(3)), down: map[tkey]int64{}}
	for b := 0; b < 100; b++ { // 下游逐批应用，任一元组前缀多重性不得为负
		d, _ := f.feed()
		for _, x := range d {
			k := tkey{x.K, x.A, x.B}
			f.down[k] += x.Mult
			bad(t, f.down[k] < 0, "negative after prefix batch %d: %v=%d", b, k, f.down[k])
		}
	}
	bad(t, slices.IndexFunc(slices.Concat(f.e.r.Snapshot(), f.e.s.Snapshot()), func(x rel.Triple) bool { return x.Mult <= 0 }) >= 0, "zero row retained") // 内存不得残留0重行
}
func TestRejectedBatchAtomic(t *testing.T) {
	cs := []struct {
		dr, ds []rel.Row
		e      error
	}{
		{[]rel.Row{rw(9, "z", -1)}, []rel.Row{rw(9, "q", 1)}, ErrDeleteMissing}, // 同批另一表变更须一起回滚
		{nil, []rel.Row{rw(9, "q", -1)}, ErrDeleteMissing},
		{[]rel.Row{rw(3, "a", 2)}, nil, ErrInvalidChange},
		{[]rel.Row{rw(3, "", 1)}, nil, ErrInvalidChange},
		{[]rel.Row{rw(2, "a", 1)}, []rel.Row{rw(2, "p", 1)}, ErrViewLimit},
	}
	for i, c := range cs {
		e := New(1) // 预置 1 元组正好顶到 maxView
		e.Feed([]rel.Row{rw(1, "a", 1)}, []rel.Row{rw(1, "p", 1)})
		v, err := e.Feed(c.dr, c.ds)
		bad(t, !errors.Is(err, c.e) || len(v) != 0 || asMap(e.View())[tkey{1, "a", "p"}] != 1, "case %d: %v %v %v", i, err, v, e.View())
	}
	e2 := New(10) // 被拒之后引擎仍可继续正常使用
	_, ea := e2.Feed(nil, []rel.Row{rw(1, "p", -1)})
	d, eb := e2.Feed([]rel.Row{rw(1, "a", 1)}, []rel.Row{rw(1, "p", 1)})
	bad(t, !errors.Is(ea, ErrDeleteMissing) || eb != nil || len(d) != 1, "after reject: %v %v", ea, eb)
}
func TestCheckedRowsSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} { // m 档循环；检查行数须恒为真正命中数 1，不随 m 增长
		e := New(m*2 + 10)
		dR, dS := make([]rel.Row, 0, m), make([]rel.Row, 0, m)
		for i := 1; i <= m; i++ {
			dR = append(dR, rw(int64(i), "a", 1))
			dS = append(dS, rw(int64(i), "p", 1))
		}
		e.Feed(dR, dS)
		d, err := e.Feed([]rel.Row{rw(42, "n", 1)}, nil)
		bad(t, err != nil || len(d) != 1 || e.checked != 1, "m=%d checked=%d d=%v %v", m, e.checked, d, err)
	}
}
func TestConcurrentFeeds(t *testing.T) {
	e, N := New(100000), 64
	var badf, stop atomic.Bool
	var wg, rwg sync.WaitGroup
	rwg.Add(1)
	go func() { // 并发读者：每个 K 要么不存在，要么恰是最终 (K,a,p):1
		defer rwg.Done()
		for !stop.Load() {
			for _, d := range e.View() {
				badf.CompareAndSwap(false, d.Mult != 1 || d.A != "a" || d.B != "p")
			}
		}
	}()
	for i := 0; i < N; i++ { // K 互不相交的插入批
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e.Feed([]rel.Row{rw(int64(1000+i), "a", 1)}, []rel.Row{rw(int64(1000+i), "p", 1)})
		}(i)
	}
	wg.Wait()
	stop.Store(true)
	rwg.Wait()
	bad(t, badf.Load() || len(asMap(e.View())) != N || !same(e.View(), recompute(e.r, e.s)), "half batch/negative/final mismatch")
}
