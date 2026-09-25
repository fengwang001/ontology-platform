package cache

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

var configs = []struct{ raw, align, slab int }{{10, 8, 60}, {1, 1, 1}, {8, 8, 64}, {7, 4, 100}, {3, 16, 48}}

// runOps plays n random ops, checking inv.1/3 after each (inv.2 if nv set).
func runOps(t *testing.T, c *Cache, nv *naive, seed int64, n int) []int {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var live []int
	for step := 0; step < n; step++ {
		if len(live) == 0 || rng.Intn(2) == 0 {
			off, _ := c.Alloc()
			if in := off % c.slabSize; in%c.sz != 0 || in+c.sz > c.slabSize {
				t.Fatalf("packing: bad offset %d", off)
			}
			if nv != nil {
				if want := nv.alloc(); off != want {
					t.Fatalf("naive: got %d want %d", off, want)
				}
			}
			live = append(live, off)
		} else {
			k := rng.Intn(len(live))
			off := live[k]
			live = append(live[:k], live[k+1:]...)
			_ = c.Free(off) // a miss desyncs live[] and trips conservation
			if nv != nil {
				nv.free(off)
			}
		}
		if st := c.Stats(); st.Allocated+st.Free != c.perSlab*st.Slabs || st.Allocated != len(live) {
			t.Fatalf("conservation: %+v live=%d", st, len(live))
		}
	}
	return live
}

func runAll(t *testing.T, seed int64, useNaive bool) {
	for i, cfg := range configs {
		c, _ := New(cfg.raw, cfg.align, cfg.slab)
		var nv *naive
		if useNaive {
			nv = &naive{slabSize: c.slabSize, sz: c.sz, perSlab: c.perSlab}
		}
		live := runOps(t, c, nv, seed+int64(i), 400)
		if !useNaive {
			continue
		}
		for _, off := range live { // drain: nothing allocated, no full slab
			_ = c.Free(off)
		}
		if st := c.Stats(); st.Allocated != 0 || st.Full != 0 {
			t.Fatalf("drained: %+v", st)
		}
	}
}

func TestConservation(t *testing.T)   { runAll(t, 0, false) }    // invariant 1
func TestPacking(t *testing.T)        { runAll(t, 1000, false) } // invariant 3
func TestNaiveReference(t *testing.T) { runAll(t, 2000, true) }  // invariant 2

// TestFailureAtomic: invariant 4 — distinct sentinels, no state change.
func TestFailureAtomic(t *testing.T) {
	bads := []badCtor{
		{0, 8, 60, ErrRawSize}, {10, 3, 60, ErrAlign}, {10, 8, 8, ErrTooBig},
	}
	for _, b := range bads {
		if _, err := New(b.raw, b.align, b.slab); err != b.want {
			t.Fatalf("%v: got %v want %v", b, err, b.want)
		}
	}
	c, _ := New(10, 8, 60)
	live, _ := c.Alloc()
	before := c.Stats()
	for _, off := range []int{999, 48} { // never allocated; tail waste
		if err := c.Free(off); err != ErrBadFree {
			t.Fatalf("Free(%d): %v", off, err)
		}
	}
	if c.Stats() != before {
		t.Fatal("rejected frees changed state")
	}
	if err := c.Free(live); err != nil {
		t.Fatal(err)
	}
	if err := c.Free(live); err != ErrBadFree { // double free
		t.Fatal("double free not rejected")
	}
}

// TestComplexityConstant: slabs inspected per Alloc/Free is O(1) in m
// (white-box: reads the unexported counter directly).
func TestComplexityConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c, _ := New(10, 8, 60)
		offs := make([]int, 0, m*c.perSlab)
		for i := 0; i < m*c.perSlab; i++ { // fill m slabs completely
			off, _ := c.Alloc()
			offs = append(offs, off)
		}
		for j := 0; j < m; j++ { // one free slot per slab: m partial slabs
			_ = c.Free(offs[j*c.perSlab])
		}
		_, _ = c.Alloc()
		if c.checked > 4 {
			t.Fatalf("m=%d: Alloc checked %d slabs", m, c.checked)
		}
		_ = c.Free(offs[1])
		if c.checked > 1 {
			t.Fatalf("m=%d: Free checked %d slabs", m, c.checked)
		}
	}
}

// TestConcurrent: N goroutines alloc then free their own offset; readers
// see counts in [0,N]; the end state has zero allocated.
func TestConcurrent(t *testing.T) {
	c, _ := New(10, 8, 60)
	const n = 64
	var stop, bad atomic.Bool
	go func() {
		for !stop.Load() {
			if v := c.Stats().Allocated; v < 0 || v > n {
				bad.Store(true)
			}
		}
	}()
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if off, err := c.Alloc(); err == nil {
				c.Free(off)
			}
		}()
	}
	wg.Wait()
	stop.Store(true)
	if bad.Load() || c.Stats().Allocated != 0 {
		t.Fatal("concurrency broke conservation")
	}
}
