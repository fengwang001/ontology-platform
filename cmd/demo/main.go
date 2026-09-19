package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology"
)

type report struct {
	pass  int
	total int
}

func (r *report) check(name string, ok bool) {
	r.total++
	status := "FAIL"
	if ok {
		status = "OK"
		r.pass++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	var result report
	base := time.Unix(1000, 0)

	stepwise, _ := ontology.NewLimiter(ontology.Config{Rate: 7, Capacity: 100})
	oneShot, _ := ontology.NewLimiter(ontology.Config{Rate: 7, Capacity: 100})
	_ = stepwise.Allow("t", 100, base)
	_ = oneShot.Allow("t", 100, base)
	now := base
	for range 10 {
		now = now.Add(100 * time.Millisecond)
		_ = stepwise.Available("t", now)
	}
	result.check("fractional refill: 10x100ms equals 1s",
		stepwise.Available("t", now) == 7 &&
			oneShot.Available("t", base.Add(time.Second)) == 7)

	capped, _ := ontology.NewLimiter(ontology.Config{Rate: 10, Capacity: 5})
	_ = capped.Allow("t", 5, base)
	result.check("refill is capped at capacity", capped.Available("t", base.Add(time.Hour)) == 5)

	rewind, _ := ontology.NewLimiter(ontology.Config{Rate: 5, Capacity: 3})
	_ = rewind.Allow("t", 3, base)
	after := base.Add(time.Second)
	before := rewind.Available("t", after)
	result.check("rewound time is rejected and leaves state unchanged",
		errors.Is(rewind.Allow("t", 1, base), ontology.ErrTimeRewound) &&
			before == 3 && rewind.Available("t", base) == before)

	edges, _ := ontology.NewLimiter(ontology.Config{Rate: 7, Capacity: 5})
	_ = edges.Allow("t", 5, base)
	zeroOK := edges.Allow("t", 0, base) == nil
	negative := errors.Is(edges.Allow("t", -1, base), ontology.ErrNegativeTokens)
	overCapacity := errors.Is(edges.Allow("t", 6, base), ontology.ErrRequestExceedsCapacity)
	result.check("n=0 allowed; negative and over-capacity rejected distinctly",
		zeroOK && negative && overCapacity)

	waiting, _ := ontology.NewLimiter(ontology.Config{Rate: 7, Capacity: 5})
	_ = waiting.Allow("t", 5, base)
	rejectedAt := base.Add(100 * time.Millisecond)
	err := waiting.Allow("t", 3, rejectedAt)
	wait, hasWait := ontology.RetryAfter(err)
	readyAt := rejectedAt.Add(wait)
	_, earlyWait := ontology.RetryAfter(waiting.Allow("t", 3, readyAt.Add(-time.Nanosecond)))
	result.check("retry wait matches the actual refill instant",
		hasWait && earlyWait && waiting.Allow("t", 3, readyAt) == nil)

	isolated, _ := ontology.NewLimiter(ontology.Config{Rate: 5, Capacity: 3})
	_ = isolated.Allow("a", 3, base)
	_ = isolated.Allow("b", 1, base)
	result.check("tenants are isolated",
		isolated.Available("a", base) == 0 && isolated.Available("b", base) == 2)

	evictable, _ := ontology.NewLimiter(ontology.Config{
		Rate: 5, Capacity: 3, IdleTTL: time.Second,
	})
	_ = evictable.Allow("idle", 3, base)
	evictable.EvictInactive(base.Add(time.Second + time.Nanosecond))
	recreatedFull := evictable.Available("idle", base.Add(time.Second+time.Nanosecond))
	result.check("evicted inactive tenant restarts with a full bucket",
		evictable.ActiveTenants() == 1 && recreatedFull == 3)

	concurrent, _ := ontology.NewLimiter(ontology.Config{
		Rate: 10, Capacity: 10, ShardCount: 256,
	})
	var wg sync.WaitGroup
	var allowed atomic.Int64
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if concurrent.Allow("same", 1, base) == nil {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	result.check("concurrent requests never overshoot the same tenant",
		allowed.Load() == 10 && concurrent.Available("same", base) == 0)

	fmt.Printf("TOTAL %d/%d checks passed\n", result.pass, result.total)
}
