// Command demo exercises the Voronoi nearest-site package end to end.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/sites"
)

var failed bool

func ok(name string, cond bool) {
	tag := "OK"
	if !cond {
		tag, failed = "FAIL", true
	}
	fmt.Println(tag, name)
}

func main() {
	if err := api.New(); err != nil {
		panic(err)
	}
	a0, _ := api.Add(0, 0)
	a1, _ := api.Add(1, 1)
	a2, _ := api.Add(4, 0)
	ok("1-3 Add(0,0),(1,1),(4,0) -> 0,1,2", a0 == 0 && a1 == 1 && a2 == 2)
	n4, _ := api.Nearest(0, 2)
	ok("4 Nearest(0,2)=1 (d2 4,2,20; manhattan would wrongly give 0)", n4 == 1)
	n5, _ := api.Nearest(1, 0)
	ok("5 Nearest(1,0)=0 (tie d2=1 -> min index; <=-last would give 1)", n5 == 0)
	a3, _ := api.Add(0, 2)
	n7, _ := api.Nearest(0, 2)
	w8, _ := api.Within(1, 0, 1)
	ok("6-8 Add(0,2)=3 Nearest=3 Within(1,0,1)=0 (<= would count s0,s1=2)",
		a3 == 3 && n7 == 3 && w8 == 0)
	ok("naive-scan agreement, ties, rejects, concurrency (api.SelfCheck)", api.SelfCheck() == nil)

	_, eDup := api.Add(0, 0)
	_, eOOB := api.Add(10001, 0)
	_, eNeg := api.Within(0, 0, -1)
	distinct := errors.Is(eDup, sites.ErrDuplicate) && !errors.Is(eDup, sites.ErrOutOfBounds) &&
		errors.Is(eOOB, sites.ErrOutOfBounds) && !errors.Is(eOOB, sites.ErrBadRadius) &&
		errors.Is(eNeg, sites.ErrBadRadius) && !errors.Is(eNeg, sites.ErrDuplicate)
	ok("three distinct decidable errors: duplicate / out-of-bounds / negative r", distinct)

	before, _ := api.Within(0, 0, 100000)
	api.Add(0, 0)
	api.Add(10001, 0)
	after, _ := api.Within(0, 0, 100000)
	_, stillUsable := api.Add(-7, 13)
	ok("rejected ops leave no trace and registry stays usable", before == after && stillUsable == nil)
	ok("candidate count bounded for m in 100..10000", sites.CheckSublinear() == nil)

	const N = 24
	var wg sync.WaitGroup
	res := make([]int, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g], _ = api.Nearest(7, -3) }(g)
	}
	wg.Wait()
	agree := true
	for g := 1; g < N; g++ {
		agree = agree && res[g] == res[0]
	}
	ok("24 concurrent readers return identical results", agree)

	if failed {
		panic("demo checks failed")
	}
}
