// Command demo exercises the LogLog cardinality estimator and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/est"
	"ontology/lg"
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

func main() {
	// Section 3: seven injected (bucket, z) pairs on m=8.
	r := lg.New(8)
	steps := [][2]int{{0, 0}, {2, 2}, {2, 4}, {5, 1}, {0, 3}, {5, 5}, {2, 3}}
	want := [][8]int{{1, 0, 0, 0, 0, 0, 0, 0}, {1, 0, 3, 0, 0, 0, 0, 0}, {1, 0, 5, 0, 0, 0, 0, 0}, {1, 0, 5, 0, 0, 2, 0, 0}, {4, 0, 5, 0, 0, 2, 0, 0}, {4, 0, 5, 0, 0, 6, 0, 0}, {4, 0, 5, 0, 0, 6, 0, 0}}
	ok := true
	for i, s := range steps {
		r.Add(s[0], s[1])
		for j := 0; j < 8; j++ {
			if r.At(j) != want[i][j] {
				ok = false
			}
		}
	}
	check("seven-step registers", ok)

	// Estimate after the seven steps: mean = 15/8, value = α_8·8·2^1.875.
	e := est.New(8)
	for _, s := range steps {
		e.Add(s[0], s[1])
	}
	got := e.Estimate()
	wantEst := est.Alpha(8) * 8 * math.Exp2(15.0/8.0)
	check(fmt.Sprintf("estimate=%.4f", got), got == wantEst)

	// Estimate reads exactly m registers regardless of element count.
	check("estimate visits m registers", est.CheckVisited(8) == nil)

	// api-level checks.
	c, err := api.New(8)
	mono := err == nil
	prev := c.Registers()
	for _, s := range steps {
		if c.Add(s[0], s[1]) != nil {
			mono = false
		}
		cur := c.Registers()
		for j := range cur {
			if cur[j] < prev[j] {
				mono = false
			}
		}
		prev = cur
	}
	check("monotonic", mono)

	one, _ := api.New(8)
	_ = one.Add(3, 4)
	reg := one.Registers()
	single := reg[3] == 5
	for j, v := range reg {
		if j != 3 && v != 0 {
			single = false
		}
	}
	check("single element exact", single)

	replay, _ := api.New(8)
	naive := make([]int, 8)
	for _, s := range steps {
		_ = replay.Add(s[0], s[1])
		if r := s[1] + 1; r > naive[s[0]] {
			naive[s[0]] = r
		}
	}
	same := true
	for j, v := range replay.Registers() {
		if v != naive[j] {
			same = false
		}
	}
	check("naive replay identical", same)

	_, e0 := api.New(0)
	_, e3 := api.New(3)
	eb := c.Add(8, 0)
	ez := c.Add(0, -1)
	distinct := errors.Is(e0, api.ErrInvalidM) && errors.Is(e3, api.ErrInvalidM) &&
		errors.Is(eb, api.ErrBucketOutOfRange) && errors.Is(ez, api.ErrInvalidZ) &&
		api.ErrInvalidM != api.ErrBucketOutOfRange &&
		api.ErrBucketOutOfRange != api.ErrInvalidZ && api.ErrInvalidM != api.ErrInvalidZ
	check("three distinct sentinel errors", distinct)

	before := c.Registers()
	_ = c.Add(-1, 0)
	_ = c.Add(8, 0)
	_ = c.Add(0, -1)
	notrace := true
	for j, v := range c.Registers() {
		if v != before[j] {
			notrace = false
		}
	}
	check("rejected adds leave no trace", notrace)

	const g = 16
	vals := make([]float64, g)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vals[i] = c.Estimate()
		}(i)
	}
	wg.Wait()
	agree := true
	for _, v := range vals {
		if v != vals[0] {
			agree = false
		}
	}
	check("concurrent estimates agree", agree)

	check("selfcheck", c.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
