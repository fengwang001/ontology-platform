// demo 逐条打印各项核验的 OK/FAIL，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/vv"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func same(a, b vv.Vector) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func main() {
	a := api.New()
	ok := a.Set("r1", vv.Vector{0: 1}) == nil &&
		a.Set("r2", vv.Vector{0: 1, 1: 2}) == nil &&
		a.Set("r3", vv.Vector{1: 1, 2: 1}) == nil
	check("S1-S3 set r1,r2,r3", ok)

	m4, e4 := a.Merge("r1", "r2")
	check(fmt.Sprintf("S4 merge(r1,r2)=%v", m4), e4 == nil && same(m4, vv.Vector{0: 1, 1: 2}))
	m5, e5 := a.Merge("r2", "r3")
	check(fmt.Sprintf("S5 merge(r2,r3)=%v", m5), e5 == nil && same(m5, vv.Vector{0: 1, 1: 2, 2: 1}))
	c6, e6 := a.Compare("r1", "r2")
	check(fmt.Sprintf("S6 compare(r1,r2)=%v", c6), e6 == nil && c6 == vv.Less)
	c7, e7 := a.Compare("r1", "r3")
	check(fmt.Sprintf("S7 compare(r1,r3)=%v", c7), e7 == nil && c7 == vv.Concurrent)

	m21, _ := a.Merge("r2", "r1")
	m11, _ := a.Merge("r1", "r1")
	check("join commutative+idempotent, selfcheck", same(m4, m21) && same(m11, vv.Vector{0: 1}) && a.SelfCheck() == nil)

	e1 := a.Set("bad", vv.Vector{0: -1})
	e2 := a.Set("bad", vv.Vector{-1: 0})
	_, e3 := a.Merge("r1", "ghost")
	_, e3b := a.Compare("ghost", "r1")
	check("3 distinct sentinel errors", errors.Is(e1, api.ErrNegativeCounter) &&
		errors.Is(e2, api.ErrNegativeActor) && errors.Is(e3, api.ErrUnknownName) &&
		errors.Is(e3b, api.ErrUnknownName) && e1 != e2 && e2 != e3 && e1 != e3)

	again, _ := a.Merge("r1", "r2")
	stillBad := a.Set("bad", vv.Vector{5: 5})
	check("registry unchanged after rejects", same(again, m4) && stillBad == nil)

	check("sparse merge reads bounded (vv.TestMergeReadsBounded)", func() bool {
		for _, m := range []int{100, 1000, 10000} {
			r := vv.Merge(vv.Vector{0: 1, m - 1: 2}, vv.Vector{1: 3, m - 2: 4})
			if len(r) != 4 {
				return false
			}
		}
		return true
	}())

	const n = 64
	results := make([]vv.Vector, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = a.Merge("r2", "r3")
		}(i)
	}
	wg.Wait()
	consistent := true
	for _, r := range results {
		if !same(r, m5) {
			consistent = false
		}
	}
	check("concurrent merge results identical", consistent)

	if failed {
		os.Exit(1)
	}
}
