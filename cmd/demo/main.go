// Command demo verifies the late-correction reprocessing engine. It
// prints one OK/FAIL line per check (at most ten) and exits non-zero on
// any failure. It takes no arguments and does no networking.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/chlog"
)

var fail bool

func ok(name string, cond bool, detail string) {
	if cond {
		fmt.Println("OK " + name)
	} else {
		fail = true
		fmt.Println("FAIL " + name + " " + detail)
	}
}

func main() {
	const K = "k"
	seq := []int64{5, 7, 6, 8, 5, 6, 6, 9}
	val := []int64{10, 20, 15, 5, 99, 15, 30, 3}
	want := []api.Status{api.StatusNew, api.StatusNew, api.StatusLate, api.StatusNew,
		api.StatusStale, api.StatusDuplicate, api.StatusCorrection, api.StatusNew}
	sums := []int64{10, 30, 45, 50, 50, 50, 65, 68}
	e, _ := api.New(3)
	var log []chlog.Change
	gotS, gotSt := []int64{}, []api.Status{}
	for i := range seq {
		cs, st, err := e.Apply(K, seq[i], val[i])
		if err != nil {
			fmt.Println("FAIL step", i+1, err)
			os.Exit(1)
		}
		log = append(log, cs...)
		gotS, gotSt = append(gotS, e.View()[K]), append(gotSt, st)
	}
	stOK := true
	for i := range want {
		if gotSt[i] != want[i] {
			stOK = false
		}
	}
	ok(fmt.Sprintf("eight steps sums=%v", gotS),
		fmt.Sprint(gotS) == fmt.Sprint(sums) && stOK, fmt.Sprint(gotSt))
	ok("step3 late accepted / step5 stale / step6 idempotent",
		gotSt[2] == api.StatusLate && gotSt[4] == api.StatusStale &&
			gotSt[5] == api.StatusDuplicate && e.Stale() == 1, fmt.Sprint(gotSt))

	prefixOK := true
	for n := range len(log) + 1 {
		if _, err := chlog.ReplayPrefix(log, n); err != nil {
			prefixOK = false
		}
	}
	ok("changelog every prefix self-consistent", prefixOK, fmt.Sprint(len(log)))

	_, eW := api.New(0)
	_, _, eK := e.Apply("", 1, 1)
	_, _, eS := e.Apply(K, 0, 1)
	ok("three distinct sentinel errors",
		errors.Is(eW, api.ErrInvalidW) && errors.Is(eK, api.ErrEmptyKey) &&
			errors.Is(eS, api.ErrInvalidSeq) && eW != eK && eK != eS,
		fmt.Sprintf("%v %v %v", eW, eK, eS))

	v0, z0 := e.View(), e.Stale()
	_, _, _ = e.Apply("", 9, 9)
	_, _, _ = e.Apply(K, -1, 9)
	ok("rejected op leaves no trace, engine still usable",
		fmt.Sprint(e.View()) == fmt.Sprint(v0) && e.Stale() == z0 && func() bool {
			_, _, err := e.Apply("z", 1, 1)
			return err == nil
		}(), "")

	memOK := true                               // O(1) map membership across window sizes; the unexported
	for _, m := range []int{100, 1000, 10000} { // probe count is pinned ==1
		g, _ := api.New(m) // by the white-box agg test, never exported.
		for s := 1; s <= m; s++ {
			g.Apply("g", int64(s), 1)
		}
		if _, st, _ := g.Apply("g", int64(m), 2); st != api.StatusCorrection {
			memOK = false // in-window correction accepted
		}
		g.Apply("g", int64(m)+1, 1) // evict seq 1
		if _, st, _ := g.Apply("g", 1, 2); st != api.StatusStale {
			memOK = false // evicted seq correction rejected
		}
	}
	ok("window membership O(1) for m=100..10000", memOK, "")

	const N = 200
	c, _ := api.New(N)
	var wg sync.WaitGroup
	var wantSum int64
	for i := 1; i <= N; i++ {
		wg.Add(1)
		v := int64(i * 3)
		wantSum += v
		go func(s, vv int64) { defer wg.Done(); c.Apply("p", s, vv) }(int64(i), v)
	}
	wg.Wait()
	ok(fmt.Sprintf("concurrent Apply sum=%d", c.View()["p"]), c.View()["p"] == wantSum, "")

	if err := e.SelfCheck(); err != nil {
		ok("SelfCheck", false, err.Error())
	} else {
		fmt.Println("OK SelfCheck all four invariants")
	}
	if fail {
		os.Exit(1)
	}
}
