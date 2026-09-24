package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"ontology/api"
)

var failures int

func report(name string, ok bool) {
	if !ok {
		failures++
	}
	fmt.Printf("%s %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

// checkSevenSteps replays the section-3 sequence, returning the counter
// state after each step and whether every verdict is right.
func checkSevenSteps() ([][7]uint8, bool) {
	f, err := api.New(7, 3, 2)
	if err != nil {
		return nil, false
	}
	var states [][7]uint8
	ok := true
	step := func(g func() bool) {
		ok = g() && ok
		var a [7]uint8
		copy(a[:], f.Snapshot())
		states = append(states, a)
	}
	step(func() bool { return f.Insert(1) == nil })
	step(func() bool { return f.Insert(4) == nil })
	step(func() bool { return f.Insert(3) == nil })
	step(func() bool { return f.Insert(3) == api.ErrOverflow })
	step(func() bool { return f.Contains(2) })
	step(func() bool { return f.Delete(2) == api.ErrNotInserted })
	step(func() bool { return f.Contains(1) })
	return states, ok
}

// checkInvariants: no false negatives, conservation, distinct errors, atomic rejection.
func checkInvariants() (noFN, cons, errs, atomicity bool) {
	f, err := api.New(101, 5, 255)
	if err != nil {
		return
	}
	net, cons, noFN, atomicity := 0, true, true, true
	for x := int64(0); x < 40; x++ {
		if f.Insert(x) != nil {
			return
		}
		net++
		cons = cons && f.Sum() == 5*net
	}
	for x := int64(0); x < 40; x += 2 {
		if f.Delete(x) != nil {
			return
		}
		net--
		cons = cons && f.Sum() == 5*net
	}
	for x := int64(1); x < 40; x += 2 {
		noFN = noFN && f.Contains(x)
	}
	g, _ := api.New(7, 3, 2)
	_ = g.Insert(3)
	_ = g.Insert(3)
	before := g.Snapshot()
	e1, e2 := g.Insert(3), g.Delete(2)
	_, e3 := api.New(6, 3, 2)
	errs = errors.Is(e1, api.ErrOverflow) && errors.Is(e2, api.ErrNotInserted) &&
		errors.Is(e3, api.ErrInvalidParams) && !errors.Is(e1, api.ErrNotInserted) &&
		!errors.Is(e2, api.ErrOverflow) && !errors.Is(e3, api.ErrOverflow)
	after := g.Snapshot()
	for i := range before {
		atomicity = atomicity && before[i] == after[i]
	}
	atomicity = atomicity && g.Contains(3) && g.Insert(2) == nil
	return
}

// checkAccessCount runs the in-package cbf test: touched counters stay == k for m up to 10000.
func checkAccessCount() bool {
	return exec.Command("go", "test", "-count=1", "-run", "TestAccessCountConstant", "./cbf/").Run() == nil
}

// checkConcurrent: N goroutines Contains the same key batch; all agree per key.
func checkConcurrent() bool {
	f, err := api.New(509, 7, 255)
	if err != nil {
		return false
	}
	for x := int64(0); x < 200; x++ {
		if f.Insert(x) != nil {
			return false
		}
	}
	want := make([]bool, 400)
	for i := range want {
		want[i] = f.Contains(int64(i))
	}
	const n = 16
	got := make([][]bool, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := make([]bool, 400)
			for rep := 0; rep < 50; rep++ {
				for i := range r {
					r[i] = f.Contains(int64(i))
				}
			}
			got[g] = r
		}(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		for i := range want {
			if got[g][i] != want[i] {
				return false
			}
		}
	}
	return true
}

func main() {
	states, sevenOK := checkSevenSteps()
	report(fmt.Sprintf("seven-step counters %v", states), sevenOK)
	noFN, cons, errs, atomicity := checkInvariants()
	report("no-false-negative", noFN)
	report("counter-conservation", cons)
	report("three-distinct-errors", errs)
	report("rejected-ops-atomic", atomicity)
	report("access-count-independent-of-m", checkAccessCount())
	report("concurrent-contains", checkConcurrent())
	report("SelfCheck", api.SelfCheck() == nil)
	if failures > 0 {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
