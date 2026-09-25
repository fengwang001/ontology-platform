package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/slab"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// slab package: packing math for rawSize=10, align=8, slabSize=60
	sz := slab.AlignUp(10, 8)
	check("sz=16 perSlab=3 waste=12",
		sz == 16 && slab.PerSlab(60, sz) == 3 && slab.Waste(60, sz) == 12)
	check("align boundary: 8 stays 8", slab.AlignUp(8, 8) == 8)
	ok := true // no slot crosses a slab boundary; tail waste never used
	for j := 0; j < 4; j++ {
		for i := 0; i < slab.PerSlab(60, sz); i++ {
			off := slab.Offset(j, i, 60, sz)
			if off%60+sz > 60 || (off%60)%sz != 0 {
				ok = false
			}
		}
	}
	check("packing stays inside slab", ok)

	// cache/api: the eight operations from NOTES.md, offsets + slab states
	c, err := api.NewCache(10, 8, 60)
	if err != nil {
		check("eight steps", false)
		os.Exit(1)
	}
	wantOff := []int{0, 16, 32, 60, -1, -1, 16, 60}
	wantSt := [][3]int{{0, 1, 0}, {0, 1, 0}, {1, 0, 0}, {1, 1, 0},
		{0, 2, 0}, {0, 1, 1}, {1, 0, 1}, {1, 1, 0}} // full, partial, empty
	ok = true
	for step := 0; step < 8; step++ {
		var off int
		switch step {
		case 4:
			err = c.Free(16)
		case 5:
			err = c.Free(60)
		default:
			off, err = c.Alloc()
		}
		st := c.Stats()
		if err != nil || (wantOff[step] >= 0 && off != wantOff[step]) ||
			st.Full != wantSt[step][0] || st.Partial != wantSt[step][1] || st.Empty != wantSt[step][2] {
			ok = false
		}
	}
	check("eight steps: offsets and slab states", ok)

	// four distinguishable sentinel errors
	e1, e2, e3 := func() error { _, e := api.NewCache(0, 8, 60); return e }(),
		func() error { _, e := api.NewCache(10, 3, 60); return e }(),
		func() error { _, e := api.NewCache(10, 8, 8); return e }()
	e4 := c.Free(12345)
	check("four distinct sentinel errors",
		e1 == api.ErrRawSize && e2 == api.ErrAlign && e3 == api.ErrTooBig && e4 == api.ErrBadFree &&
			e1 != e2 && e2 != e3 && e3 != e4)

	// rejected ops leave no trace; cache keeps working
	before := c.Stats()
	c.Free(999999) // never allocated
	c.Free(48)     // inside slab0's tail waste: never a live object
	after := c.Stats()
	off, err := c.Alloc()
	check("rejected ops leave state unchanged", before == after && err == nil && off == 76)
	_ = c.Free(off)

	// invariants 1-3 and the complexity bound, on built-in sequences
	check("invariants: conservation, naive-model, packing", api.SelfCheck() == nil)
	check("slabs checked per op is O(1) up to m=10000", api.SelfCheck() == nil)

	// concurrency: N goroutines alloc then free their own offset
	cc, _ := api.NewCache(10, 8, 60)
	const n = 64
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := cc.Alloc()
			if err == nil {
				_ = cc.Stats()
				_ = cc.Free(o)
			}
		}()
	}
	wg.Wait()
	check("concurrent alloc/free conserves", cc.Stats().Allocated == 0)

	if failed {
		os.Exit(1)
	}
}
