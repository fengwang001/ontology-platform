package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology/limiter"
	"ontology/policy"
)

func main() {
	clk := &clock{now: time.Unix(1_700_000_000, 0)}
	checkBothLevels(clk)
	checkZeroChangeOnReject(clk)
	checkDistinguishableCauses(clk)
	checkContinuousRefillAndCap(clk)
	checkClockRewind(clk)
	checkBurstReject(clk)
	checkShrinkTruncates(clk)
	checkIsolationAndGlobalFloor(clk)
	checkPureInspect(clk)
	checkConcurrentNoOversell(clk)
	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
}

func checkBothLevels(clk *clock) {
	l := newLimiter(10, 0, clk, map[string]policy.Quota{"a": policy.Must(6, 0)})
	ok := l.Allow("a", 2) == nil && tenantBal(l, "a") == 4 && globalBal(l, "a") == 8
	check("both levels pass -> both deducted", ok)
}

func checkZeroChangeOnReject(clk *clock) {
	l := newLimiter(5, 0, clk, map[string]policy.Quota{"a": policy.Must(10, 0)})
	must(l.Allow("a", 3)) // global now 2, tenant 7
	t0, g0 := tenantBal(l, "a"), globalBal(l, "a")
	err := l.Allow("a", 5) // tenant ok (7), global short (2)
	ok := errors.Is(err, limiter.ErrGlobalQuota) &&
		tenantBal(l, "a") == t0 && globalBal(l, "a") == g0
	check("reject leaves both balances unchanged", ok)
}

func checkDistinguishableCauses(clk *clock) {
	l := newLimiter(4, 0, clk, map[string]policy.Quota{"a": policy.Must(2, 0)})
	must(l.Allow("a", 2)) // tenant 0, global 2
	errTenant := l.Allow("a", 1)
	must(l.Register("b", policy.Must(4, 0)))
	must(l.Allow("b", 2))      // global 0
	errBoth := l.Allow("b", 3) // tenant 2 < 3 and global 0 < 3
	errGhost := l.Allow("ghost", 1)
	ok := errors.Is(errTenant, limiter.ErrTenantQuota) &&
		!errors.Is(errTenant, limiter.ErrGlobalQuota) &&
		errors.Is(errBoth, limiter.ErrTenantQuota) && // tenant-first
		errors.Is(errGhost, limiter.ErrTenantNotFound)
	check("rejection causes distinguishable, tenant-first", ok)
}

func checkContinuousRefillAndCap(clk *clock) {
	l := newLimiter(10, 4, clk, map[string]policy.Quota{"a": policy.Must(10, 4)})
	must(l.Allow("a", 10))
	clk.Advance(1500 * time.Millisecond) // +6 tokens
	half := tenantBal(l, "a") == 6
	clk.Advance(time.Hour) // huge refill, capped at 10
	check("continuous refill and cap", half && tenantBal(l, "a") == 10)
}

func checkClockRewind(clk *clock) {
	l := newLimiter(10, 5, clk, map[string]policy.Quota{"a": policy.Must(10, 5)})
	must(l.Allow("a", 6)) // tenant 4
	clk.Advance(time.Second)
	before := tenantBal(l, "a") // 9
	clk.Advance(-time.Hour)     // rewind must be ignored
	check("clock rewind does not deduct", tenantBal(l, "a") == before)
	clk.Advance(time.Hour) // restore clock for later scenarios
}

func checkBurstReject(clk *clock) {
	l := newLimiter(10, 0, clk, map[string]policy.Quota{"a": policy.Must(4, 0)})
	err := l.Allow("a", 5)
	zero := l.Allow("a", 0)
	ok := errors.Is(err, limiter.ErrExceedsBurst) &&
		errors.Is(zero, limiter.ErrInvalidAmount) &&
		tenantBal(l, "a") == 4 && globalBal(l, "a") == 10
	check("n over capacity rejected immediately", ok)
}

func checkShrinkTruncates(clk *clock) {
	l := newLimiter(100, 0, clk, map[string]policy.Quota{"a": policy.Must(10, 0)})
	must(l.SetQuota("a", policy.Must(3, 0)))
	shrunk := tenantBal(l, "a") == 3
	must(l.SetQuota("a", policy.Must(50, 0)))
	check("shrink truncates, grow does not top up", shrunk && tenantBal(l, "a") == 3)
}

func checkIsolationAndGlobalFloor(clk *clock) {
	l := newLimiter(3, 0, clk, map[string]policy.Quota{
		"a": policy.Must(2, 0),
		"b": policy.Must(10, 0),
	})
	must(l.Allow("a", 2)) // a exhausted, global 1
	isolated := errors.Is(l.Allow("a", 1), limiter.ErrTenantQuota) &&
		l.Allow("b", 1) == nil // b unaffected; global now 0
	floor := errors.Is(l.Allow("b", 1), limiter.ErrGlobalQuota)
	check("tenant isolation and global floor", isolated && floor)
}

func checkPureInspect(clk *clock) {
	l := newLimiter(10, 1, clk, map[string]policy.Quota{"a": policy.Must(8, 2)})
	must(l.Allow("a", 3))
	t1, g1, q1, ok1 := l.Inspect("a")
	t2, g2, q2, ok2 := l.Inspect("a")
	_, _, _, ok3 := l.Inspect("ghost")
	check("inspect is pure and stable",
		ok1 && ok2 && !ok3 && t1 == t2 && g1 == g2 && q1 == q2 && t1 == 5)
}

func checkConcurrentNoOversell(clk *clock) {
	const capA, capG = 400, 600
	l := newLimiter(capG, 0, clk, map[string]policy.Quota{
		"a": policy.Must(capA, 0),
		"b": policy.Must(capA, 0),
	})
	var gotA, gotB atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tenant, counter := "a", &gotA
			if w%2 == 1 {
				tenant, counter = "b", &gotB
			}
			for i := 0; i < 200; i++ {
				if l.Allow(tenant, 2) == nil {
					counter.Add(2)
				}
			}
		}(w)
	}
	wg.Wait()
	total := gotA.Load() + gotB.Load()
	ok := gotA.Load() <= capA && gotB.Load() <= capA && total <= capG &&
		total+int64(globalBal(l, "a")) == capG
	check("concurrent Allow never oversells", ok)
}
