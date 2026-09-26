package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/samp"
	sel "ontology/select"
)

var failed bool

type kase struct {
	n, s int
	r    float64
}

func check(name string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// 第三节推导：N=10, s=4, r=1.0, d=2.5 -> [1 3 6 8]
	idx := samp.Indices(10, 4, 1.0)
	check("sec3 indices [1 3 6 8]", reflect.DeepEqual(idx, []int{1, 3, 6, 8}))

	vals := []int64{0, 10, 20, 30, 40, 50, 60, 70, 80, 90}
	got, err := sel.New(10, 4, 1.0).Sample(vals)
	check("sample values [10 30 60 80]", err == nil && reflect.DeepEqual(got, []int64{10, 30, 60, 80}))

	check("exactly s indices", exactS())
	inB, naiv := boundsAndNaive()
	check("in-bounds strictly increasing", inB)
	check("matches naive reference", naiv)
	check("three distinct decidable errors", errorsOK())
	check("rejected ops leave no trace", noTrace(vals))
	check("large-N sample size stays s", largeN())
	check("concurrent Indices identical", concurrentOK())
	sm, err := api.New(10, 4, 1.0)
	check("SelfCheck", err == nil && sm.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

func exactS() bool {
	for _, c := range [][2]int{{1, 1}, {10, 4}, {100, 10}, {97, 97}, {1000, 7}} {
		sm, err := api.New(c[0], c[1], 0)
		if err != nil || len(sm.Indices()) != c[1] {
			return false
		}
	}
	return true
}

func boundsAndNaive() (inB, naiv bool) {
	inB, naiv = true, true
	cases := []kase{{10, 4, 1.0}, {100, 10, 5.5}, {97, 97, 0}, {1000, 1, 999.9}, {100, 10, 3.25}, {7, 7, 0}}
	for _, c := range cases {
		sm, err := api.New(c.n, c.s, c.r)
		if err != nil {
			return false, false
		}
		idx := sm.Indices()
		d := float64(c.n) / float64(c.s)
		for i, ix := range idx {
			if ix < 0 || ix >= c.n || (i > 0 && ix <= idx[i-1]) {
				inB = false
			}
			if ix != int(math.Floor(c.r+float64(i)*d)) {
				naiv = false
			}
		}
	}
	return inB, naiv
}

func errorsOK() bool {
	_, e1 := api.New(10, 0, 0)
	_, e2 := api.New(10, 11, 0)
	_, e3 := api.New(10, 4, -1)
	_, e4 := api.New(10, 4, 2.5) // r == d must be rejected
	sm, _ := api.New(10, 4, 1.0)
	_, e5 := sm.Sample(make([]int64, 9))
	ok := errors.Is(e1, api.ErrSampleSize) && errors.Is(e2, api.ErrSampleSize) &&
		errors.Is(e3, api.ErrOffset) && errors.Is(e4, api.ErrOffset) &&
		errors.Is(e5, api.ErrPopulationLength)
	return ok && e1 != e3 && e3 != e5 && e1 != e5
}

func noTrace(vals []int64) bool {
	sm, _ := api.New(10, 4, 1.0)
	before := sm.Indices()
	if _, err := sm.Sample(make([]int64, 7)); err == nil {
		return false
	}
	if !reflect.DeepEqual(sm.Indices(), before) {
		return false
	}
	out, err := sm.Sample(vals) // still usable afterwards
	return err == nil && len(out) == 4
}

func largeN() bool {
	for _, n := range []int{100, 1000, 10000} {
		sm, err := api.New(n, 10, 0)
		if err != nil {
			return false
		}
		out, err := sm.Sample(make([]int64, n))
		if err != nil || len(out) != 10 {
			return false
		}
	}
	return true
}

func concurrentOK() bool {
	sm, _ := api.New(1000, 25, 3.75)
	want := sm.Indices()
	var wg sync.WaitGroup
	bad := make(chan bool, 64)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !reflect.DeepEqual(sm.Indices(), want) {
				bad <- true
			}
		}()
	}
	wg.Wait()
	return len(bad) == 0
}
