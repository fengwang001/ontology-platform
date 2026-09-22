// Command demo exercises the multi-tenant two-level rate limiter and
// prints one OK/FAIL verdict per semantic guarantee, then a total.
package main

import (
	"fmt"
	"sync"
	"time"

	"ontology/limiter"
	"ontology/policy"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Time() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
		return
	}
	failed++
	fmt.Printf("FAIL %s\n", name)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func tenantBal(l *limiter.Limiter, tenant string) float64 {
	tb, _, _, _ := l.Inspect(tenant)
	return tb
}

func globalBal(l *limiter.Limiter, tenant string) float64 {
	_, gb, _, _ := l.Inspect(tenant)
	return gb
}

func newLimiter(globalCap, globalRate float64, clk *clock, tenants map[string]policy.Quota) *limiter.Limiter {
	l := limiter.New(policy.Must(globalCap, globalRate), clk.Time)
	for name, q := range tenants {
		must(l.Register(name, q))
	}
	return l
}
