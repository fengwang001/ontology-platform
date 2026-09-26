// Command demo exercises the sliding-window rate limiter and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/lim"
	"ontology/win"
)

var fails int

func ok(cond bool, msg string) {
	tag := "OK"
	if !cond {
		tag, fails = "FAIL", fails+1
	}
	fmt.Printf("%s: %s\n", tag, msg)
}

func main() {
	// 1) Eight-step trace from NOTES.md, verdict + accepted set per step.
	l := lim.New(3, 10)
	seq := []int64{0, 2, 5, 7, 10, 11, 12, 20}
	want := []bool{true, true, true, false, false, true, false, true}
	trace, good := "", true
	for i, t := range seq {
		got, err := l.Allow(t)
		set, _ := l.Snapshot()
		good = good && err == nil && got == want[i]
		v := "D"
		if got {
			v = "A"
		}
		trace += fmt.Sprintf("%d:%s%v ", t, v, set)
	}
	ok(good, "8 steps verdicts+sets: "+trace)

	// 2) Equivalence with a naive full-scan reference on a fixed sequence.
	r, _ := api.New(3, 10)
	var ref []int64
	var now int64
	equiv := true
	for i := 0; i < 200; i++ {
		now += int64((i*7 + 3) % 3) // deterministic non-decreasing clock
		got, _ := r.Allow(now)
		k := 0
		for _, ts := range ref {
			if ts >= now-10 && ts <= now {
				k++
			}
		}
		if got != (k < 3) {
			equiv = false
		}
		if got {
			ref = append(ref, now)
		}
	}
	ok(equiv, "decisions match naive reference")

	// 3) Expired entries are popped from the queue.
	q := &win.Queue{}
	for _, ts := range []int64{0, 2, 5} {
		q.Push(ts)
	}
	q.EvictExpired(11, 10)
	ok(q.Len() == 2 && q.InWindow(11) == 2, "expired entries evicted")

	// 4) Eviction probes stay constant as in-window m grows.
	ok(win.CheckProbeBound() == nil, "probes bounded for m in {100,1k,10k}")

	// 5) Clock rollback is rejected (monotonicity).
	rm, _ := api.New(2, 10)
	rm.Allow(10)
	_, errBack := rm.Allow(9)
	ok(errors.Is(errBack, lim.ErrClockRollback), "clock rollback rejected")

	// 6) Three distinct sentinel errors.
	_, errCfg := api.New(0, 10)
	_, errNeg := rm.Allow(-1)
	distinct := errors.Is(errCfg, api.ErrInvalidConfig) &&
		errors.Is(errNeg, lim.ErrNegativeTime) &&
		!errors.Is(errCfg, errNeg) && !errors.Is(errNeg, errBack) &&
		!errors.Is(errCfg, errBack)
	ok(distinct, "three distinct sentinel errors")

	// 7) Rejections leave no trace, then the limiter stays usable.
	l2 := lim.New(3, 10)
	l2.Allow(5)
	before, lastBefore := l2.Snapshot()
	l2.Allow(-1)
	l2.Allow(4)
	after, lastAfter := l2.Snapshot()
	usable, _ := l2.Allow(6)
	noTrace := fmt.Sprint(after) == fmt.Sprint(before) && lastAfter == lastBefore && usable
	ok(noTrace, "rejected calls leave no trace; still usable")

	// 8) Concurrent same-timestamp callers: admits <= limit == Accepted().
	rc, _ := api.New(1, 10)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var admit int64
	var mu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if a, _ := rc.Allow(7); a {
				mu.Lock()
				admit++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	ok(admit <= 1 && rc.Accepted() == int(admit), "concurrent admits <= limit")

	// 9) Built-in self-check of all four invariants.
	rs, _ := api.New(3, 10)
	ok(rs.SelfCheck() == nil, "SelfCheck passes")

	if fails > 0 {
		fmt.Println("FAIL: demo detected", fails, "failures")
	}
}
