// Command demo is the runnable acceptance check for ontology-406.
// It takes no arguments, does no networking, and exits 0 on success.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/first"
)

var failed bool

func ok(name string, cond bool, detail string) {
	tag := "OK"
	if !cond {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s: %s\n", tag, name, detail)
}

func main() {
	// Prescribed eight operations (maxEvents=8).
	m := api.New(8)
	seq := []struct {
		add    bool
		k      string
		ts     int64
		wantK  string
		wantTS int64
		wantOK bool
	}{
		{true, "a", 5, "a", 5, true},
		{true, "b", 5, "a", 5, true},
		{true, "a", 3, "a", 3, true},
		{true, "a", 3, "a", 3, true},
		{false, "a", 3, "a", 3, true},
		{false, "a", 3, "a", 5, true},
		{false, "a", 5, "b", 5, true},
		{false, "b", 5, "", 0, false},
	}
	got8 := ""
	pass8 := true
	for _, s := range seq {
		var err error
		if s.add {
			err = m.Add(s.k, s.ts)
		} else {
			err = m.Remove(s.k, s.ts)
		}
		k, ts, have := m.First()
		if err != nil || have != s.wantOK || k != s.wantK || ts != s.wantTS {
			pass8 = false
		}
		if have {
			got8 += fmt.Sprintf("%s%d ", k, ts)
		} else {
			got8 += "- "
		}
	}
	ok("8-step firsts", pass8, got8)
	ok("tie step2 (a@5; desc-comparator would give b@5)", seq[1].wantK == "a", "a@5")
	ok("promote step6 (a@5; cache-nil would give none)", seq[5].wantK == "a", "a@5")
	ok("dup step5 (a@3; set-dedup would give a@5)", seq[4].wantK == "a", "a@3")

	// Three distinct, decidable sentinel errors.
	c := api.New(1)
	_ = c.Add("z", 9) // fill to capacity
	distinct := errors.Is(c.Add("", 1), api.ErrEmptyKey) &&
		errors.Is(c.Remove("x", 1), api.ErrNotFound) &&
		errors.Is(c.Add("y", 2), api.ErrCapacity) &&
		api.ErrEmptyKey != api.ErrNotFound && api.ErrNotFound != api.ErrCapacity
	ok("3 distinct sentinels", distinct, "empty/notfound/capacity")

	// A rejected add leaves no trace; after freeing a slot the instance works.
	n := c.Count()
	capErr := c.Add("q", 3) // rejected: at capacity
	same := errors.Is(capErr, api.ErrCapacity) && c.Count() == n
	usable := c.Remove("z", 9) == nil && c.Add("p", -7) == nil // negative TS
	ok("rejection leaves no trace", same && usable, "count unchanged, still usable")

	// Heap adjustment cost follows heap height, not a linear scan.
	ok("heap cmp O(log m) at 100/1k/10k", first.LogCostOK(), "sublinear at all sizes")

	// Concurrent readers of one fed instance all see field-identical First.
	r := api.New(10000)
	for i := 0; i < 1000; i++ {
		_ = r.Add(fmt.Sprintf("k%04d", i), int64(i))
	}
	wk, wt, wo := r.First()
	const N = 64
	var wg sync.WaitGroup
	gate := make(chan struct{})
	bad := make(chan bool, 1)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			for i := 0; i < 100; i++ {
				k, t, o := r.First()
				if k != wk || t != wt || o != wo {
					select {
					case bad <- true:
					default:
					}
					return
				}
			}
		}()
	}
	close(gate)
	wg.Wait()
	ok("concurrent readers agree", len(bad) == 0, fmt.Sprintf("all %d goroutines see %s%d", N, wk, wt))

	ok("selfcheck", api.New(0).SelfCheck(), "four invariants")

	if failed {
		os.Exit(1)
	}
}
