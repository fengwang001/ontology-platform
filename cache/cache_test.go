package cache

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

var cfgs = [][3]int{{10, 8, 60}, {8, 8, 64}, {1, 1, 5}, {12, 4, 40}, {3, 8, 30}}

func drive(t *testing.T, cfg [3]int, steps int) (*Cache, []int) {
	t.Helper()
	c, _ := NewCache(cfg[0], cfg[1], cfg[2])
	g := c.geom
	ref := &naiveRef{size: g.Size, per: g.PerSlab, slab: g.SlabSize}
	rng := rand.New(rand.NewSource(int64(cfg[0]*131 + steps)))
	var live []int
	for s := 0; s < steps; s++ {
		if n := len(live); n > 0 && rng.Intn(2) == 1 {
			k := rng.Intn(n)
			off := live[k]
			live = append(live[:k], live[k+1:]...)
			_ = c.Free(off)
			ref.free(off)
			continue
		}
		off, _ := c.Alloc()
		if want := ref.alloc(); off != want {
			t.Fatalf("cfg %v step %d alloc %d want naive %d", cfg, s, off, want)
		}
		live = append(live, off)
	}
	return c, live
}
func verifyDriven(t *testing.T) {
	for _, cfg := range cfgs { // verify pins conservation/census and Contains packing
		c, _ := drive(t, cfg, 1500)
		if err := c.verify(); err != nil {
			t.Fatalf("cfg %v: %v", cfg, err)
		}
	}
}
func TestConservation(t *testing.T) { verifyDriven(t) }
func TestPacking(t *testing.T)      { verifyDriven(t) }
func TestNaiveEquivalence(t *testing.T) {
	for _, cfg := range cfgs {
		c, live := drive(t, cfg, 1500)
		if err := c.SelfCheck(); err != nil {
			t.Fatalf("selfcheck: %v", err)
		}
		for _, o := range live {
			_ = c.Free(o)
		}
		if st := c.Stats(); st.Allocated != 0 || st.Full != 0 {
			t.Fatalf("drain left state: %+v", st)
		}
	}
}
func TestErrorsAtomic(t *testing.T) {
	for _, tc := range []struct {
		r, a, s int
		w       error
	}{
		{0, 8, 60, ErrBadSize},
		{1, 3, 60, ErrBadAlign},
		{100, 8, 60, ErrDoesNotFit},
	} {
		if _, err := NewCache(tc.r, tc.a, tc.s); !errors.Is(err, tc.w) {
			t.Fatalf("cfg %+v: %v", tc, err)
		}
	}
	c, _ := NewCache(10, 8, 60)
	before := c.Stats()
	for _, off := range []int{0, 16, 60, 48} {
		if err := c.Free(off); !errors.Is(err, ErrInvalidFree) {
			t.Fatalf("free %d: %v", off, err)
		}
	}
	if c.Stats() != before {
		t.Fatal("rejected Free mutated state")
	}
	o, _ := c.Alloc()
	_ = c.Free(o)
	if err := c.Free(o); !errors.Is(err, ErrInvalidFree) {
		t.Fatalf("duplicate free: %v", err)
	}
}
func TestComplexityConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c, _ := NewCache(10, 8, 64)
		for i := 0; i < m*4; i++ {
			_, _ = c.Alloc()
		}
		for j := 0; j < m; j++ {
			_ = c.Free(j * 64)
		}
		if off, _ := c.Alloc(); off != 0 || c.checks > 2 {
			t.Fatalf("m=%d off=%d checked=%d not constant", m, off, c.checks)
		}
		if err := c.Free(16); err != nil || c.checks != 1 {
			t.Fatalf("m=%d free checked=%d err=%v", m, c.checks, err)
		}
	}
}
func TestConcurrentAllocFree(t *testing.T) {
	const N = 64
	c, _ := NewCache(8, 8, 4096)
	offs := make([]int, N)
	start, stop := make(chan struct{}), make(chan struct{})
	var rwg, wg sync.WaitGroup
	rwg.Add(1)
	go func() { // alloc-only ramp: a concurrent reader never sees a decrease
		defer rwg.Done()
		prev := 0
		for {
			select {
			case <-stop:
				return
			default:
				if n := c.Stats().Allocated; n >= prev {
					prev = n
				} else {
					t.Errorf("live count decreased %d->%d", prev, n)
				}
			}
		}
	}()
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) { defer wg.Done(); <-start; offs[i], _ = c.Alloc() }(i)
	}
	close(start)
	wg.Wait()
	close(stop) // stop and fully join the reader before any Free
	rwg.Wait()
	if c.Stats().Allocated != N {
		t.Fatalf("after ramp live=%d want %d", c.Stats().Allocated, N)
	}
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(o int) { defer wg.Done(); _ = c.Free(o) }(offs[i])
	}
	wg.Wait()
	if c.Stats().Allocated != 0 {
		t.Fatal("concurrent alloc+free did not return to zero")
	}
}
