package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"ontology/api"
	"ontology/bidx"
)

var failed bool

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

func naive(ts []int64, T int64) (int64, bool) {
	run, res, found := int64(0), int64(-1), false
	for o, v := range ts {
		if o == 0 || v > run {
			run = v
		}
		if run <= T {
			res, found = int64(o), true
		}
	}
	return res, found
}

func main() {
	seq := []int64{2, 1, 8, 3, 4, 9, 5, 7}
	x := api.New(8)
	for _, v := range seq {
		_ = x.Append(v)
	}
	bounds := map[int64]int64{1: -1, 2: 1, 7: 1, 8: 4, 9: 7, 100: 7}
	pmOK := true
	for T, want := range bounds {
		off, found, _ := x.SafeOff(T)
		if off != want || found != (want >= 0) {
			pmOK = false
		}
	}
	for i, v := range seq {
		got, _ := x.TSAt(int64(i))
		if got != v {
			pmOK = false
		}
	}
	ok("pm after 8 appends: [2 2 8 8 8 9 9 9]", pmOK)

	off7, _, _ := x.SafeOff(7)
	off8, _, _ := x.SafeOff(8)
	off2, _, _ := x.SafeOff(2)
	t0, _ := x.TSAt(0)
	ok("SafeOff(7/8/2)=(1,4,1) and TSAt(0)=2", off7 == 1 && off8 == 4 && off2 == 1 && t0 == 2)

	eCap := x.Append(99)
	_, eNeg := x.TSAt(-1)
	_, eEnd := x.TSAt(8)
	empty := api.New(8)
	_, _, eEmpty := empty.SafeOff(0)
	distinct := errors.Is(eCap, api.ErrCapacity) && errors.Is(eNeg, bidx.ErrOutOfRange) &&
		errors.Is(eEnd, bidx.ErrOutOfRange) && errors.Is(eEmpty, bidx.ErrEmptyLog) &&
		api.ErrCapacity != bidx.ErrOutOfRange && bidx.ErrOutOfRange != bidx.ErrEmptyLog
	ok("three distinct sentinel errors", distinct)

	noTrace := x.Len() == 8
	_ = empty.Append(1) // empty-log rejection left the index usable
	if l := empty.Len(); l != 1 {
		noTrace = false
	}
	ok("rejected ops leave no trace; index still usable", noTrace)

	r := rand.New(rand.NewSource(408))
	n := 10000
	big := api.New(n)
	ts := make([]int64, n)
	for i := range ts {
		ts[i] = r.Int63n(1000) - 500
		_ = big.Append(ts[i])
	}
	const reps = 2000
	start := time.Now()
	for k := 0; k < reps; k++ {
		big.SafeOff(int64(k%1000) - 500)
	}
	bin := time.Since(start)
	start = time.Now()
	for k := 0; k < reps; k++ {
		naive(ts, int64(k%1000)-500)
	}
	lin := time.Since(start)
	ok("large N: SafeOff cost sub-linear vs naive scan", bin < lin/10)

	var wg sync.WaitGroup
	same := true
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for o := int64(0); o < int64(n); o++ {
				if v, err := big.TSAt(o); err != nil || v != ts[o] {
					same = false
				}
			}
			for _, T := range []int64{-500, -1, 0, 250, 499} {
				a, fa, _ := big.SafeOff(T)
				b, fb, _ := big.SafeOff(T)
				if a != b || fa != fb {
					same = false
				}
			}
		}()
	}
	wg.Wait()
	ok("concurrent readers get identical results", same)

	if failed {
		os.Exit(1)
	}
}
