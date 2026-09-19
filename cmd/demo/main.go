// Command demo exercises the ontology rate limiter end to end with a fake
// clock: no real time, no network, no arguments. Each scenario prints one
// OK/FAIL line and the program exits non-zero if anything fails.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology"
)

var failures int

func check(name string, ok bool, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %-28s %s\n", verdict, name, detail)
}

func main() {
	base := time.Unix(1_000_000, 0)

	// 1. Fractional refill: 10x100ms at 7/s equals one 1s step, exactly 7.
	steps := newLimiter(1000, 7)
	once := newLimiter(1000, 7)
	steps.Allow("t", 1000, base)
	once.Allow("t", 1000, base)
	now := base
	for i := 0; i < 10; i++ {
		now = now.Add(100 * time.Millisecond)
	}
	a := steps.Available("t", now)
	b := once.Available("t", base.Add(time.Second))
	check("fractional refill", a == 7 && b == 7, fmt.Sprintf("10x100ms=%d, 1x1s=%d", a, b))

	// 2. Refill never exceeds capacity.
	capped := newLimiter(10, 100)
	capped.Allow("t", 10, base)
	got := capped.Available("t", base.Add(time.Hour))
	check("refill capped", got == 10, fmt.Sprintf("after 1h idle: %d/10", got))

	// 3. Backward time: zero elapsed, no negative refill, no reset.
	back := newLimiter(10, 5)
	back.Allow("t", 10, base)
	back.Available("t", base.Add(time.Second))
	d, err := back.Allow("t", 3, base.Add(500*time.Millisecond))
	ok := err == nil && d.Allowed && back.Available("t", base.Add(500*time.Millisecond)) == 2
	check("backward time", ok, "earlier now: no refill, no panic, no reset")

	// 4. n = 0 / negative / over capacity.
	edge := newLimiter(10, 5)
	d0, e0 := edge.Allow("t", 0, base)
	_, eNeg := edge.Allow("t", -1, base)
	_, eBig := edge.Allow("t", 11, base)
	ok = e0 == nil && d0.Allowed && errors.Is(eNeg, ontology.ErrNegativeN) &&
		errors.Is(eBig, ontology.ErrExceedsCapacity)
	check("n=0/neg/over-cap", ok, "0: allowed, -1: ErrNegativeN, 11>10: ErrExceedsCapacity")

	// 5. Reported wait matches the real refill time.
	waiter := newLimiter(10, 4)
	waiter.Allow("t", 10, base)
	d, _ = waiter.Allow("t", 2, base)
	early, _ := waiter.Allow("t", 2, base.Add(d.Wait-time.Nanosecond))
	later, _ := waiter.Allow("t", 2, base.Add(d.Wait))
	check("wait duration", !d.Allowed && !early.Allowed && later.Allowed,
		fmt.Sprintf("need 2 @4/s -> wait %v, then allowed", d.Wait))

	// 6. Tenants are isolated.
	iso := newLimiter(5, 0)
	iso.Allow("alice", 5, base)
	_, aliceErr := iso.Allow("alice", 1, base)
	bob := iso.Available("bob", base)
	check("tenant isolation", aliceErr == nil && bob == 5, "alice drained, bob still 5/5")

	// 7. Idle tenants are reclaimed and restart full.
	rec := newLimiter(5, 1)
	rec.Allow("ghost", 5, base)
	removed := rec.ReclaimIdle(base.Add(10*time.Minute), 5*time.Minute)
	d, _ = rec.Allow("ghost", 5, base.Add(10*time.Minute))
	ok = removed == 1 && rec.ActiveTenants() == 1 && d.Allowed
	check("reclaim idle", ok, fmt.Sprintf("reclaimed %d, active %d, back full", removed, rec.ActiveTenants()))

	// 8. Concurrent same-tenant traffic never oversells.
	hot := newLimiter(1000, 0)
	var granted atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if d, err := hot.Allow("hot", 1, base); err == nil && d.Allowed {
					granted.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	check("no oversell", granted.Load() == 1000,
		fmt.Sprintf("3200 requests vs 1000 tokens: granted %d", granted.Load()))

	verdict := "OK  "
	if failures != 0 {
		verdict = "FAIL"
	}
	fmt.Printf("----\n%s total: 8 checks, %d failed\n", verdict, failures)
	if failures != 0 {
		os.Exit(1)
	}
}

func newLimiter(capacity, rate int64) *ontology.Limiter {
	l, err := ontology.NewLimiter(capacity, rate)
	if err != nil {
		fmt.Println("FAIL setup:", err)
		os.Exit(1)
	}
	return l
}
