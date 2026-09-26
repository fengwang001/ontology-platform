// Command demo prints OK/FAIL lines for every deliverable check.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/wrr"
)

var failed bool

func report(name string, ok bool) {
	if !ok {
		failed = true
	}
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// 1. six-step pick sequence for weights [3,1,2]
	b, err := api.New([]int{3, 1, 2})
	seq := make([]int, 6)
	for k := range seq {
		seq[k] = b.Next()
	}
	report(fmt.Sprintf("six-step seq %v", seq), err == nil && fmt.Sprint(seq) == "[0 2 0 1 2 0]")

	// 2. fairness: over W picks server i is chosen exactly w_i times
	w := []int{2, 5, 3, 7, 1}
	f, _ := api.New(w)
	total := 0
	for _, x := range w {
		total += x
	}
	got := make([]int, len(w))
	for k := 0; k < total; k++ {
		got[f.Next()]++
	}
	ok := true
	for i := range w {
		ok = ok && got[i] == w[i]
	}
	report("fairness: W picks give exactly w_i each", ok)

	// 3. sum(cw)==0 after every Next
	core := wrr.New([]int{3, 1, 2})
	ok = true
	for k := 0; k < 60; k++ {
		core.Next()
		ok = ok && core.SumCW() == 0
	}
	report("sum(cw)==0 after every Next", ok)

	// 4. matches the naive expand-and-count reference
	var expanded []int
	for i, x := range w {
		for k := 0; k < x; k++ {
			expanded = append(expanded, i)
		}
	}
	naive := make([]int, len(w))
	for _, s := range expanded {
		naive[s]++
	}
	ok = true
	for i := range w {
		ok = ok && got[i] == naive[i]
	}
	report("matches naive weighted-count reference", ok)

	// 5. three distinct rejectable errors
	_, e1 := api.New(nil)
	_, e1b := api.New([]int{1, 0})
	d, _ := api.New([]int{3, 1, 2})
	e2 := d.SetWeight(5, 1)
	e3 := d.SetWeight(0, 0)
	ok = errors.Is(e1, api.ErrInvalidConfig) && errors.Is(e1b, api.ErrInvalidConfig) &&
		errors.Is(e2, api.ErrIndexOutOfRange) && errors.Is(e3, api.ErrInvalidWeight) &&
		!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3)
	report("3 distinct sentinel errors", ok)

	// 6. state intact after rejected calls
	ctrl, _ := api.New([]int{3, 1, 2})
	ok = d.Weight(0) == 3 && d.Weight(1) == 1 && d.Weight(2) == 2
	for k := 0; k < 12 && ok; k++ {
		ok = d.Next() == ctrl.Next()
	}
	report("state intact after rejected calls", ok)

	// 7. max-location checks independent of m
	report("max-location checks bounded (heap, not scan)", wrr.CheckScalability() == nil)

	// 8. concurrent Next counts stay fair
	cb, _ := api.New([]int{3, 1, 2})
	counts := make([]atomic.Int64, 3)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				counts[cb.Next()].Add(1)
			}
		}()
	}
	wg.Wait()
	ok = counts[0].Load() == 1200 && counts[1].Load() == 400 && counts[2].Load() == 800
	report("concurrent Next counts fair", ok)

	// 9. SelfCheck covers the four invariants plus scalability
	report("SelfCheck (4 invariants + scalability)", b.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
