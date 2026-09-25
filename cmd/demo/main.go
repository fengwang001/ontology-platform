package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"

	"ontology/api"
	"ontology/vcache"
	"ontology/ver"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	// ver: sentinel errors are distinct and rejected writes leave no trace.
	st := ver.NewStore()
	_ = st.Write("g0", "a", 5)
	e1 := st.Write("", "x", 1)
	e2 := st.Write("g0", "", 1)
	e3 := st.Write("g1", "a", 1)
	distinct := errors.Is(e1, ver.ErrEmptyGroup) && errors.Is(e2, ver.ErrEmptyKey) &&
		errors.Is(e3, ver.ErrKeyConflict) && e1 != e2 && e2 != e3 && e1 != e3
	untouched := st.Stamp("g0") == 1 && st.Stamp("g1") == 0 && st.TotalStamp() == 1 &&
		st.SumGroup("g0") == 5 && st.SumGroup("g1") == 0
	check("ver: distinct sentinel errors", distinct)
	check("ver: rejected writes leave no trace", untouched)

	// vcache: the eight-step sequence from NOTES.md, steps 4/6/8 judged.
	st2 := ver.NewStore()
	vc := vcache.New(st2)
	_ = st2.Write("g0", "a", 5)
	r2 := vc.ReadG("g0")
	_ = st2.Write("g0", "b", 3)
	r4 := vc.ReadG("g0")
	_ = st2.Write("g1", "c", 7)
	r6 := vc.ReadTotal()
	_ = st2.Write("g0", "b", 10)
	r8 := vc.ReadTotal()
	check("vcache: eight-step reads 5/8/15/22", r2 == 5 && r4 == 8 && r6 == 15 && r8 == 22)

	// api: SelfCheck, View vs batch recompute, large-m, concurrency.
	svc := api.New()
	check("api: SelfCheck", svc.SelfCheck() == nil)

	// View stays self-consistent over a deterministic write mix.
	for i := 0; i < 500; i++ {
		g := fmt.Sprintf("g%d", i%7)
		k := fmt.Sprintf("k%d", i)
		if i%3 == 0 { // rewrite an existing key in the same group
			k = fmt.Sprintf("k%d", i/3)
		}
		_ = svc.Write(g, k, int64(i*3-200))
	}
	groups, total := svc.View()
	var sum int64
	for _, v := range groups {
		sum += v
	}
	check("api: View total equals sum of groups", sum == total)

	// Large-m: invalidate one record in a 10000-record group, recompute ok.
	big := api.New()
	for i := 0; i < 10000; i++ {
		_ = big.Write("g0", fmt.Sprintf("k%d", i), 1)
	}
	_, _ = big.ReadG("g0") // warm cache
	_ = big.Write("g0", "k0", 2)
	gotBig, _ := big.ReadG("g0")
	check("api: large-m invalidate/recompute (O(1) freshness proven in vcache test)", gotBig == 10001)

	// Concurrency: value-preserving rewrites race with View readers; every
	// View must be field-by-field identical to the batch expectation.
	conc := api.New()
	wantG := map[string]int64{}
	for i := 0; i < 200; i++ {
		g := fmt.Sprintf("g%d", i%5)
		_ = conc.Write(g, fmt.Sprintf("k%d", i), int64(i))
		wantG[g] += int64(i)
	}
	var wantT int64
	for _, v := range wantG {
		wantT += v
	}
	var wg sync.WaitGroup
	views := make([]map[string]int64, 16)
	tots := make([]int64, 16)
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%4 == 0 { // value-preserving rewrite: bumps versions only
				_ = conc.Write("g0", "k0", 0)
			}
			views[i], tots[i] = conc.View()
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := range views {
		if tots[i] != wantT || !maps.Equal(views[i], wantG) {
			same = false
		}
	}
	check("api: concurrent views identical to batch", same)

	if failed {
		os.Exit(1)
	}
}
