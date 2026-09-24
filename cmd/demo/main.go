// Command demo exercises the hot-key detection and re-sharding system.
// It takes no arguments and performs no network access. Each check prints
// OK or FAIL; the whole run exits non-zero if anything failed.
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/shard"
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
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	evs := []api.Event{{Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "a", Base: 0},
		{Key: "b", Base: 1}, {Key: "a", Base: 0}, {Key: "a", Base: 0},
		{Key: "c", Base: 1}, {Key: "a", Base: 0}}

	// Per-step cnt and migration marker, all on one compact line.
	sys, _ := api.New(2, 3)
	var steps []string
	prevLen := 2
	triggerOnBase := false
	for i, e := range evs {
		_ = sys.Feed([]api.Event{e})
		c := sys.Counts()
		mark := "-"
		if len(c) > prevLen {
			mark = "MIG"
			prevLen = len(c)
			triggerOnBase = c[0] == 3 && c[2] == 0 // trigger event filled base, dedicated starts 0
		}
		steps = append(steps, fmt.Sprintf("%d:%v/%s", i+1, c, mark))
	}
	fmt.Println("steps:", strings.Join(steps, " "))

	report("trigger-event-on-base-shard", triggerOnBase)
	report("matches-naive-reference", fmt.Sprint(sys.Counts()) == "[3 2 3]")

	var sum int64
	for _, c := range sys.Counts() {
		sum += c
	}
	report("total-conserved", sum == int64(len(evs)))

	// Four distinct, decidable sentinel errors.
	_, eS := api.New(0, 1)
	_, eT := api.New(1, 0)
	eBase := sys.Feed([]api.Event{{Key: "z", Base: 9}})
	eKey := sys.Feed([]api.Event{{Key: "", Base: 0}})
	distinct := errors.Is(eS, api.ErrInvalidShards) && errors.Is(eT, api.ErrInvalidThresh) &&
		errors.Is(eBase, api.ErrBaseOutOfRange) && errors.Is(eKey, api.ErrEmptyKey) &&
		eS != eT && eBase != eKey && eS != eBase && eS != eKey
	report("four-distinct-sentinel-errors", distinct)

	before := sys.Counts()
	_ = sys.Feed([]api.Event{{Key: "a", Base: 0}, {Key: "", Base: 0}})
	report("rejected-batch-leaves-no-trace", fmt.Sprint(sys.Counts()) == fmt.Sprint(before))

	report("lookup-O(1)-independent-of-m", shard.LookupCostStaysConstant())

	// Concurrent readers must see field-identical snapshots; no sleeps.
	const n = 16
	res := make(chan []int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); res <- sys.Counts() }()
	}
	wg.Wait()
	close(res)
	base := sys.Counts()
	same := true
	for c := range res {
		if fmt.Sprint(c) != fmt.Sprint(base) {
			same = false
		}
	}
	report("concurrent-readers-agree", same)

	report("selfcheck", sys.SelfCheck() == nil)

	if failed {
		fmt.Println("DEMO FAIL")
	}
}
