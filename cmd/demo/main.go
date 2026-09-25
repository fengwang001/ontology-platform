// Command demo verifies the K-slack out-of-order counter. It takes no
// arguments, does no network I/O, and exits 0 when every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/slack"
	"ontology/wcount"
)

var failed bool

func ok(cond bool, msg string) {
	status := "OK"
	if !cond {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", status, msg)
}

func main() {
	// Package slack: walk the eight canonical events K=3 on key "a".
	seqs := []int64{10, 8, 12, 5, 8, 11, 9, 7}
	wantHigh := []int64{10, 10, 12, 12, 12, 12, 12, 12}
	var st slack.State
	var acc, drop int
	step7Boundary, highOK := false, true
	for i, seq := range seqs {
		d := slack.Decide(&st, seq, 3)
		highOK = highOK && st.High == wantHigh[i]
		switch d {
		case slack.First, slack.Accept:
			acc++
		case slack.Drop:
			drop++
		}
		if i == 6 && d == slack.Accept && st.High-seq == 3 {
			step7Boundary = true // seq == high-K must be accepted (left closed)
		}
	}
	ok(highOK && st.High == 12 && acc == 5 && drop == 3 && step7Boundary,
		"slack: 8 steps high=12 accepted=5 dropped=3; seq==high-K accepted")

	// Package wcount: per-key windows; rejected batches leave no trace.
	c, _ := wcount.New(3, 8)
	_ = c.Feed([]wcount.Event{{Key: "a", Seq: 100}, {Key: "b", Seq: 1}})
	hb, okb := c.High("b")
	ok(okb && hb == 1 && c.Accepted("a")+c.Accepted("b") == 2 && c.Dropped() == 0,
		"wcount: per-key windows; b's first event accepted after a@100")
	before := c.Dropped()
	err := c.Feed([]wcount.Event{{Key: "a", Seq: 101}, {Key: "", Seq: 1}})
	ha, _ := c.High("a")
	ok(errors.Is(err, wcount.ErrEmptyKey) && ha == 100 && c.Dropped() == before &&
		c.Accepted("a") == 1, "wcount: empty key rejects whole batch, state intact")
	err = c.Feed([]wcount.Event{{Key: "k9", Seq: 1}, {Key: "k10", Seq: 1},
		{Key: "k11", Seq: 1}, {Key: "k12", Seq: 1}, {Key: "k13", Seq: 1},
		{Key: "k14", Seq: 1}, {Key: "k15", Seq: 1}})
	ok(errors.Is(err, wcount.ErrTooManyKeys) && c.Dropped() == before,
		"wcount: maxKeys overflow rejects whole batch")

	// Package api: SelfCheck, first-event rule, in-window accept without
	// advancing high, and three distinct decidable parameter errors.
	w, _ := api.New(3, 100000)
	ok(w.SelfCheck() == nil, "api: SelfCheck passes all four invariants")
	w2, _ := api.New(3, 4)
	_ = w2.Feed([]api.Event{{Key: "a", Seq: 10}, {Key: "a", Seq: 8}})
	h2, _ := w2.High("a")
	_, eNeg := api.New(-1, 4)
	_, eZero := api.New(3, 0)
	distinct := !errors.Is(api.ErrEmptyKey, api.ErrTooManyKeys) &&
		!errors.Is(api.ErrTooManyKeys, api.ErrInvalidArgs) &&
		!errors.Is(api.ErrEmptyKey, api.ErrInvalidArgs)
	ok(w2.Accepted("a") == 2 && h2 == 10 && errors.Is(eNeg, api.ErrInvalidArgs) &&
		errors.Is(eZero, api.ErrInvalidArgs) && distinct,
		"api: first sets high, in-window accepted no advance; 3 distinct sentinels")

	// Large m: functional correctness at many key counts. The O(1) probe
	// property itself is unexported and pinned numerically by
	// wcount.TestProbeNotLinear (never readable through exported API).
	largeOK := true
	for _, m := range []int{100, 1000, 10000} {
		lm, _ := api.New(3, m+1)
		batch := make([]api.Event, m)
		for i := range batch {
			batch[i] = api.Event{Key: fmt.Sprintf("k%05d", i), Seq: int64(i + 1)}
		}
		if lm.Feed(batch) != nil || lm.Accepted(fmt.Sprintf("k%05d", m-1)) != 1 {
			largeOK = false
		}
	}
	ok(largeOK, "api: correct across m=100..10000 (probe O(1): TestProbeNotLinear)")

	// Concurrency: disjoint-key Feeders end state equal to serial; concurrent
	// readers of a prefilled instance see field-by-field identical snapshots.
	const N = 32
	par, _ := api.New(3, N*4)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := fmt.Sprintf("p%02d", g)
			_ = par.Feed([]api.Event{{Key: k, Seq: int64(g) + 2},
				{Key: k, Seq: int64(g)}, {Key: k, Seq: int64(g) + 5}})
		}(g)
	}
	filled, _ := api.New(3, N)
	rbatch := make([]api.Event, N)
	for i := range rbatch {
		rbatch[i] = api.Event{Key: fmt.Sprintf("r%02d", i), Seq: int64(i + 1)}
	}
	_ = filled.Feed(rbatch)
	snaps := make([][][2]int64, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			snaps[g] = make([][2]int64, N)
			for i := 0; i < N; i++ {
				k := fmt.Sprintf("r%02d", i)
				h, _ := filled.High(k)
				snaps[g][i] = [2]int64{h, filled.Accepted(k)}
			}
		}(g)
	}
	wg.Wait()
	concurOK := true
	for g := 0; g < N; g++ { // serial expectation: disjoint keys, all 3 accepted
		k := fmt.Sprintf("p%02d", g)
		if h, _ := par.High(k); h != int64(g)+5 || par.Accepted(k) != 3 {
			concurOK = false
		}
		for i := range snaps[g] { // every reader snapshot identical field by field
			if snaps[g][i] != snaps[0][i] {
				concurOK = false
			}
		}
	}
	ok(concurOK, "api: concurrent disjoint Feed == serial; readers identical (race clean)")

	if failed {
		os.Exit(1)
	}
}
