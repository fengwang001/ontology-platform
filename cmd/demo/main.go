// Command demo exercises the version-vector packages and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/vv"
)

func main() {
	fail := false
	check := func(label string, ok bool) {
		if ok {
			fmt.Println("OK   " + label)
		} else {
			fail = true
			fmt.Println("FAIL " + label)
		}
	}

	a := api.New()
	setOK := a.Set("r1", vv.Vector{0: 1}) == nil &&
		a.Set("r2", vv.Vector{0: 1, 1: 2}) == nil &&
		a.Set("r3", vv.Vector{1: 1, 2: 1}) == nil
	check(fmt.Sprintf("S1-S3 Set replicas -> %v", true), setOK)

	m4, e4 := a.Merge("r1", "r2")
	check(fmt.Sprintf("S4 Merge(r1,r2) = %v", m4), e4 == nil &&
		reflect.DeepEqual(m4, vv.Vector{0: 1, 1: 2}))

	m5, e5 := a.Merge("r2", "r3")
	check(fmt.Sprintf("S5 Merge(r2,r3) = %v", m5), e5 == nil &&
		reflect.DeepEqual(m5, vv.Vector{0: 1, 1: 2, 2: 1}))

	c6, _ := a.Compare("r1", "r2")
	check(fmt.Sprintf("S6 Compare(r1,r2) = %s", c6), c6 == vv.Less)

	c7, _ := a.Compare("r1", "r3")
	check(fmt.Sprintf("S7 Compare(r1,r3) = %s", c7), c7 == vv.Concurrent)

	x, y := vv.Vector{0: 1, 2: 3}, vv.Vector{1: 2, 2: 1}
	jxy, jyx := vv.Merge(x, y), vv.Merge(y, x)
	check("Merge commutative, idempotent, upper bound",
		reflect.DeepEqual(jxy, jyx) && reflect.DeepEqual(vv.Merge(x, x), x) &&
			vv.Compare(jxy, x) != vv.Less && vv.Compare(jxy, y) != vv.Less)

	ec := a.Set("negC", vv.Vector{0: -1})
	ea := a.Set("negA", vv.Vector{-1: 1})
	_, eu := a.Merge("r1", "ghost")
	check("three distinct sentinel errors on invalid input",
		errors.Is(ec, api.ErrNegativeCounter) && errors.Is(ea, api.ErrNegativeActor) &&
			errors.Is(eu, api.ErrUnknownName) && ec != ea && ec != eu && ea != eu)

	_, stillBad := a.Merge("r1", "negC")
	c6b, _ := a.Compare("r1", "r2")
	check("registry state unchanged after rejected ops",
		errors.Is(stillBad, api.ErrUnknownName) && c6b == vv.Less && a.SelfCheck() == nil)

	sparse := true
	for _, m := range []int{100, 1000, 10000} { // exact read-count bound is pinned in vv_test
		g := vv.Merge(vv.Vector{m - 1: 1, m - 2: 1}, vv.Vector{m - 3: 1, m - 4: 1})
		sparse = sparse && len(g) == 4
	}
	check("large m: sparse merge output stays constant-size", sparse)

	const n = 64
	var wg sync.WaitGroup
	results := make([]vv.Vector, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], _ = a.Merge("r2", "r3")
		}(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		same = same && reflect.DeepEqual(results[0], results[i])
	}
	check(fmt.Sprintf("concurrent merges identical across %d goroutines", n), same)

	if fail {
		os.Exit(1)
	}
}
