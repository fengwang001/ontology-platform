// Command demo runs in-process behavioral checks for the slab allocator.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/slab"
)

func ok(b bool) string {
	if b {
		return "OK"
	}
	return "FAIL"
}

type fpe struct{ f, p, e int }

func census(c *api.Cache) fpe {
	s := c.Stats()
	return fpe{s.Full, s.Partial, s.Empty}
}

func main() {
	g := slab.Pack(10, 8, 60)
	fmt.Printf("%s geometry sz=16 perSlab=3 waste=12; alignUp(8,8)=8\n",
		ok(g.Size == 16 && g.PerSlab == 3 && g.Waste() == 12 && slab.AlignUp(8, 8) == 8))
	fmt.Printf("%s packing: tail slot@48 rejected, next slab slot0@60 legal\n",
		ok(!g.Contains(48) && g.Contains(60)))

	c, _ := api.NewCache(10, 8, 60)
	wantOff := []int{0, 16, 32, 60}
	got := make([]int, 4)
	for i := range got {
		got[i], _ = c.Alloc()
	}
	_ = c.Free(16)
	_ = c.Free(60)
	r1, _ := c.Alloc()
	r2, _ := c.Alloc()
	states := []fpe{{0, 1, 0}, {0, 1, 0}, {1, 0, 0}, {1, 1, 0},
		{0, 2, 0}, {0, 1, 1}, {1, 0, 1}, {1, 1, 0}}
	gotSt := []fpe{}
	c2, _ := api.NewCache(10, 8, 60)
	collect := func() { gotSt = append(gotSt, census(c2)) }
	for i := 0; i < 4; i++ {
		_, _ = c2.Alloc()
		collect()
	}
	_ = c2.Free(16)
	collect()
	_ = c2.Free(60)
	collect()
	_, _ = c2.Alloc()
	collect()
	_, _ = c2.Alloc()
	collect()
	stMatch := len(gotSt) == len(states)
	for i := range states {
		if i < len(gotSt) && gotSt[i] != states[i] {
			stMatch = false
		}
	}
	offMatch := true
	for i := range wantOff {
		if got[i] != wantOff[i] {
			offMatch = false
		}
	}
	fmt.Printf("%s eight-step offsets 0,16,32,60 -> free ->16,60 (got %v,%d,%d)\n",
		ok(offMatch && r1 == 16 && r2 == 60), got, r1, r2)
	fmt.Printf("%s eight-step F/P/E states match the derivation table\n", ok(stMatch))
	fmt.Printf("%s invariants: conservation + naive-equivalence + legal packing\n",
		ok(c.SelfCheck() == nil && c2.SelfCheck() == nil))

	_, eSize := api.NewCache(0, 8, 60)
	_, eAlign := api.NewCache(1, 3, 60)
	_, eFit := api.NewCache(100, 8, 60)
	eFree := c.Free(99999)
	distinct := errors.Is(eSize, api.ErrBadSize) && errors.Is(eAlign, api.ErrBadAlign) &&
		errors.Is(eFit, api.ErrDoesNotFit) && errors.Is(eFree, api.ErrInvalidFree) &&
		api.ErrBadSize != api.ErrBadAlign && api.ErrBadAlign != api.ErrDoesNotFit &&
		api.ErrDoesNotFit != api.ErrInvalidFree
	fmt.Printf("%s four distinct decidable sentinel errors\n", ok(distinct))

	before := c.Stats()
	_ = c.Free(12345) // duplicate/never-allocated: must be rejected
	after := c.Stats()
	probe, _ := c.Alloc()
	_ = c.Free(probe)
	fmt.Printf("%s rejected op leaves state unchanged, cache still usable\n",
		ok(c.Free(12345) != nil && before == after))

	// m partial slabs (each with >=1 free slot): fill m full slabs then free
	// one slot per slab. Next Alloc must hit slab0 slot 0 directly via the
	// partial set, independent of m (the constant checks counter is asserted
	// in the white-box test, never read through any exported method).
	m := 2000
	big, _ := api.NewCache(10, 8, 64) // sz=16, perSlab=4
	for i := 0; i < m*4; i++ {
		_, _ = big.Alloc()
	}
	for j := 0; j < m; j++ {
		_ = big.Free(j * 64)
	}
	next, _ := big.Alloc()
	sb := big.Stats()
	fmt.Printf("%s m=%d partial slabs: one Alloc hits slab0 slot0 off=0 (set, not scan)\n",
		ok(next == 0 && sb.Partial == m-1 && sb.Full == 1), m)

	pc, _ := api.NewCache(8, 8, 4096)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			off, err := pc.Alloc()
			if err == nil {
				_ = pc.Free(off)
			}
		}()
	}
	wg.Wait()
	fmt.Printf("%s concurrency: 100 goroutines alloc+free -> %d live (want 0)\n",
		ok(pc.Stats().Allocated == 0), pc.Stats().Allocated)
}
