package api_test

import (
	"errors"
	"math/rand"
	"ontology/api"
	"ontology/tlog"
	"sync"
	"sync/atomic"
	"testing"
)

func cases(t *testing.T, f func(g *api.Store, base, mn, mx int64, s []int64)) {
	seqs := [][]int64{{50, 40, 70, 60, 70, 65, 90, 80, 90, 85}, {7}, {0, 0, 0}, {9, 8, 7, 10}}
	r := rand.New(rand.NewSource(2026))
	for k := 0; k < 12; k++ {
		s := make([]int64, r.Intn(120)+1)
		for i := range s {
			s[i] = int64(r.Intn(30))
		}
		seqs = append(seqs, s)
	}
	for idx, s := range seqs {
		g, _ := api.New(int64(idx), 1_000_000) // 序列均为非负且远小于容量，Append 必成功
		g.Append(s)
		mn, mx := s[0], s[0]
		for _, x := range s[1:] {
			mn, mx = min(mn, x), max(mx, x)
		}
		f(g, int64(idx), mn, mx, s)
	}
}
func TestNaiveReference(t *testing.T) {
	cases(t, func(g *api.Store, base, mn, mx int64, s []int64) {
		for q := mn - 1; q <= mx+1; q++ {
			got, ok := g.Lookup(q)
			wp, wok := base+int64(len(s)), false
			for i, x := range s {
				if x >= q {
					wp, wok = base+int64(i), true
					break
				}
			}
			if got != wp || ok != wok {
				t.Fatalf("q=%d (%d,%v) want (%d,%v)", q, got, ok, wp, wok)
			}
		}
	})
	g0, _ := api.New(55, 10)
	if o, f := g0.Lookup(0); o != 55 || f {
		t.Fatalf("empty (%d,%v) want (55,false)", o, f)
	}
}
func TestIndexInvariants(t *testing.T) {
	cases(t, func(g *api.Store, base, _, _ int64, s []int64) {
		var pre int64 = -1
		es := g.IndexEntries()
		for j, e := range es {
			if j > 0 && (e.TS <= es[j-1].TS || e.Offset <= es[j-1].Offset) {
				t.Fatal("index not strictly increasing")
			}
			for k := 0; k <= int(e.Offset-base); k++ {
				pre = max(pre, s[k])
			}
			if e.TS != pre || s[e.Offset-base] != e.TS {
				t.Fatalf("j=%d ts=%d pre=%d", j, e.TS, pre)
			}
		}
	})
}
func TestLookupMonotonic(t *testing.T) {
	cases(t, func(g *api.Store, _, mn, mx int64, _ []int64) {
		prev, _ := g.Lookup(mn - 1)
		for q := mn; q <= mx+1; q++ {
			cur, _ := g.Lookup(q)
			if cur < prev {
				t.Fatalf("q=%d %d<%d", q, cur, prev)
			}
			prev = cur
		}
	})
}
func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	for _, c := range [][2]int64{{-1, 10}, {0, 0}, {1, -3}} {
		if _, e := api.New(c[0], int(c[1])); !errors.Is(e, tlog.ErrInvalidParam) {
			t.Fatalf("New %v: %v", c, e)
		}
	}
	if tlog.ErrInvalidParam == tlog.ErrNegativeTS || tlog.ErrInvalidParam == tlog.ErrCapacity ||
		tlog.ErrNegativeTS == tlog.ErrCapacity {
		t.Fatal("sentinel errors must be distinct")
	}
	g, _ := api.New(10, 2)
	g.Append([]int64{5})
	leo, ni := g.LEO(), len(g.IndexEntries())
	o0, f0 := g.Lookup(0)
	want := []error{tlog.ErrNegativeTS, tlog.ErrNegativeTS, tlog.ErrCapacity, tlog.ErrCapacity}
	for i, b := range [][]int64{{-1}, {5, -9}, {6, 7}, {1, 2, 3, 4}} {
		if _, e := g.Append(b); !errors.Is(e, want[i]) {
			t.Fatalf("batch %v: %v", b, e)
		}
		if o, f := g.Lookup(0); g.LEO() != leo || len(g.IndexEntries()) != ni || o != o0 || f != f0 {
			t.Fatalf("batch %v left a trace", b)
		}
	}
	if first, e := g.Append([]int64{8}); e != nil || first != 11 {
		t.Fatalf("after reject first=%d %v", first, e)
	}
}
func TestConcurrentLookupStable(t *testing.T) {
	g, _ := api.New(100, 100000)
	var stop, fixed atomic.Int64
	fixed.Store(-1)
	var wg, seen sync.WaitGroup
	wg.Add(4)
	seen.Add(4)
	worker := func() {
		defer wg.Done()
		var once sync.Once
		for stop.Load() == 0 {
			o, ok := g.Lookup(500)
			if !ok {
				continue
			}
			if v := fixed.Load(); v == -1 {
				fixed.CompareAndSwap(-1, o)
			} else if o != v {
				t.Errorf("hit %d!=%d", o, v)
			}
			once.Do(seen.Done)
		}
	}
	for range 4 {
		go worker()
	}
	for i := 0; i < 51; i++ {
		g.Append([]int64{int64(i) * 10})
	}
	seen.Wait()
	for i := 51; i < 200; i++ {
		g.Append([]int64{int64(i) * 10})
	}
	stop.Store(1)
	wg.Wait()
	if fixed.Load() != 150 {
		t.Fatalf("fixed=%d want 150", fixed.Load())
	}
}
