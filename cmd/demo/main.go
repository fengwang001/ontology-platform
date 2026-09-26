// Command demo is a self-contained acceptance check for the systematic sampler.
// It reads no arguments, performs no networking, and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/samp"
	selectx "ontology/select"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func eqInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eq64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	const n, s = 10, 4
	z, err := api.New(n, s, 1.0)
	if err != nil {
		fmt.Println("FAIL construction:", err)
		os.Exit(1)
	}
	idx := z.Indices()

	vals := make([]int64, n)
	for i := range vals {
		vals[i] = int64(i)
	}
	got, err := z.Sample(vals)
	if err != nil {
		fmt.Println("FAIL sample:", err)
		os.Exit(1)
	}

	check("indices [1 3 6 8]", eqInt(idx, []int{1, 3, 6, 8}))
	check("sampled values [1 3 6 8]", eq64(got, []int64{1, 3, 6, 8}))
	check("exactly s indices/values", len(idx) == s && len(got) == s)

	inbounds := idx[0] >= 0 && idx[len(idx)-1] < n
	for i := 1; i < len(idx); i++ {
		inbounds = inbounds && idx[i] > idx[i-1]
	}
	check("in-bounds and strictly increasing", inbounds)

	naive := make([]int, s)
	d := float64(n) / s
	for i := range naive {
		naive[i] = int(math.Floor(1.0 + float64(i)*d))
	}
	check("matches naive reference", eqInt(idx, naive))

	_, eZero := api.New(10, 0, 0)
	_, eBig := api.New(10, 11, 0)
	_, eHi := api.New(10, 4, 2.5)
	_, eLen := z.Sample(make([]int64, 9))
	check("three distinct sentinel errors",
		errors.Is(eZero, api.ErrInvalidSize) && errors.Is(eBig, api.ErrInvalidSize) &&
			errors.Is(eHi, api.ErrOffsetOutOfRange) && errors.Is(eLen, api.ErrPopulationLengthMismatch))

	got2, err := z.Sample(vals)
	check("rejected calls leave state unchanged", err == nil && eq64(got2, got))

	constant := true
	for _, N := range []int{100, 1000, 10000} {
		pp, _ := samp.New(N, 10, 0)
		pop := make([]int64, N)
		for i := range pop {
			pop[i] = int64(i)
		}
		ss := selectx.New(pp)
		out, e := ss.Sample(pop)
		constant = constant && e == nil && len(out) == 10 && ss.VerifyLastSampleTookExactlyS()
	}
	check("large N: sample visits exactly s elements, constant in N", constant)

	check("SelfCheck passes all four invariants", z.SelfCheck())

	// N goroutines on one instance: every returned slice must be bit-identical.
	const g = 64
	res := make(chan []int, g)
	var wg sync.WaitGroup
	for k := 0; k < g; k++ {
		wg.Add(1)
		go func() { defer wg.Done(); res <- z.Indices() }()
	}
	wg.Wait()
	close(res)
	same := true
	for got := range res {
		if !eqInt(got, idx) {
			same = false
		}
	}
	check("concurrent Indices identical across goroutines", same)

	if failed {
		os.Exit(1)
	}
}
