// Command demo exercises the leaky bucket limiter and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/api"
)

func main() {
	failed := false
	check := func(name string, ok bool, detail string) {
		if ok {
			fmt.Println("OK   " + name)
			return
		}
		failed = true
		if detail != "" {
			fmt.Println("FAIL " + name + ": " + detail)
		} else {
			fmt.Println("FAIL " + name)
		}
	}

	// 1. The eight canonical steps: admission/rejection and departure times.
	l, _ := api.New(3, 5)
	ts := []int64{0, 5, 6, 10, 15, 15, 15, 21}
	wantDep := []int64{5, 10, 15, 20, 25, 30, 0, 35}
	wantAdm := []bool{true, true, true, true, true, true, false, true}
	eightOK := true
	for i, t := range ts {
		dep, adm, err := l.Submit(t)
		if err != nil || dep != wantDep[i] || adm != wantAdm[i] {
			eightOK = false
		}
	}
	check("eight-step table", eightOK, "")

	// 2-4. SelfCheck pins smoothness, capacity bound and naive-reference
	// agreement over the built-in sequences; 7 also covers drainChecks.
	check("selfcheck (smooth/capacity/naive/drain-bounded)", l.SelfCheck() == nil, "")

	// 5. Three mutually distinct, decidable sentinel errors.
	_, eCfg := api.New(0, 5)
	_, _, neg := l.Submit(-1)
	l.Submit(30)
	_, _, rewind := l.Submit(29)
	distinct := errors.Is(eCfg, api.ErrInvalidConfig) &&
		errors.Is(neg, api.ErrNegativeTime) &&
		errors.Is(rewind, api.ErrClockRewind) &&
		!errors.Is(eCfg, neg) && !errors.Is(neg, rewind)
	check("three distinct sentinel errors", distinct, "")

	// 6. State unchanged after rejection (in-system count, then still usable).
	before := l.InSystem()
	l.Submit(28) // rewind
	after := l.InSystem()
	dep, adm, err := l.Submit(30)
	check("rejection leaves no trace", after == before && err == nil && adm && dep > 0,
		fmt.Sprintf("before=%d after=%d", before, after))

	// 8. N goroutines, same timestamp: admitted <= capacity, deps unique+rising.
	const N, capV, iv = 64, 8, int64(5)
	c, _ := api.New(capV, iv)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var deps []int64
	admitted := 0
	for range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d, ok, _ := c.Submit(100); ok {
				mu.Lock()
				deps, admitted = append(deps, d), admitted+1
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	ccOK := admitted <= capV && len(deps) == admitted
	sort.Slice(deps, func(i, j int) bool { return deps[i] < deps[j] })
	for i := 1; ccOK && i < len(deps); i++ {
		if deps[i] <= deps[i-1] {
			ccOK = false
		}
	}
	check("concurrent same-t submit", ccOK, fmt.Sprintf("admitted=%d", admitted))

	if failed {
		fmt.Println("DEMO FAILED")
		return
	}
	fmt.Println("ALL OK")
}
