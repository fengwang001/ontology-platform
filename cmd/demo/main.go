// Command demo verifies the non-preemptive SJF scheduler end to end. It takes
// no arguments, does no networking and exits 0 only when every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/sched"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK: " + name)
	} else {
		fmt.Println("FAIL: " + name)
		failed = true
	}
}

// naive is a local copy of the plain tick-by-tick reference simulation.
func naive(ids []string, arrive, length []int64) []string {
	done := make([]bool, len(ids))
	out := []string{}
	for t, n := int64(0), 0; n < len(ids); {
		cur := -1
		for i := range ids {
			if !done[i] && arrive[i] <= t && (cur < 0 || length[i] < length[cur] ||
				length[i] == length[cur] && i < cur) {
				cur = i
			}
		}
		if cur < 0 {
			nt := int64(-1)
			for i := range ids {
				if !done[i] && (nt < 0 || arrive[i] < nt) {
					nt = arrive[i]
				}
			}
			t = nt
			continue
		}
		t, done[cur], n = t+length[cur], true, n+1
		out = append(out, ids[cur])
	}
	return out
}

func main() {
	a := api.New()
	specs := []struct {
		id             string
		arrive, length int64
	}{
		{"X", 0, 5}, {"L", 1, 10}, {"A", 2, 2},
		{"B", 3, 3}, {"C", 4, 2}, {"D", 5, 1},
	}
	ids, arr, ln := []string{}, []int64{}, []int64{}
	for _, s := range specs {
		if err := a.Submit(s.id, s.arrive, s.length); err != nil {
			panic(err)
		}
		ids, arr, ln = append(ids, s.id), append(arr, s.arrive), append(ln, s.length)
	}
	order := a.Run()
	want := []string{"X", "D", "A", "C", "B", "L"}
	check("six intervals and order [X D A C B L]",
		equal(order, want) && a.Intervals()[0] == (api.Interval{ID: "X", Start: 0, Finish: 5}))
	iv := a.Intervals()
	check("non-preemptive: X runs [0,5) to the end before shorter A starts at 6",
		iv[0].Finish == 5 && iv[2].ID == "A" && iv[2].Start == 6)
	check("equal length broken by registration order: A before C", order[2] == "A" && order[3] == "C")
	check("matches naive tick-by-tick reference", equal(order, naive(ids, arr, ln)))

	before := len(a.Run())
	errs := []error{a.Submit("", 0, 1), a.Submit("X", 0, 1), a.Submit("Q", 0, 0), a.Submit("Q", -1, 1)}
	distinct := errs[0] != errs[1] && errs[1] != errs[2] && errs[2] != errs[3] &&
		errors.Is(errs[1], sched.ErrDuplicateID)
	check("four distinct decidable sentinel errors", errs[0] != nil && errs[1] != nil &&
		errs[2] != nil && errs[3] != nil && distinct)
	check("rejected submits leave no trace (still 6 jobs, order unchanged)",
		len(a.Run()) == before && equal(a.Run(), want))
	check("min selection is O(log m), not a linear scan", sched.HeapSelectionIsOlogM())

	// Concurrency: N goroutines submit distinct ids; Run must complete all N
	// exactly once. WaitGroup provides ordering, no sleeps.
	c := api.New()
	const N = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = c.Submit(fmt.Sprintf("g%d", i), 0, int64(i+1)) }(i)
	}
	wg.Wait()
	co := c.Run()
	seen := map[string]int{}
	for _, id := range co {
		seen[id]++
	}
	once := len(seen) == N
	for k := range seen {
		if seen[k] != 1 {
			once = false
		}
	}
	check(fmt.Sprintf("concurrent submit: exactly %d jobs each completed once", N), len(co) == N && once)
	check("SelfCheck passes", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

func equal(a, b []string) bool {
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
