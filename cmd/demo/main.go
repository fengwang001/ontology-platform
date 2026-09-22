// Command demo exercises the multi-tenant two-level rate limiter and
// prints one OK/FAIL line per semantic, plus a final summary.
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

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

var failures int

func check(name string, cond bool) {
	if cond {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failures++
	}
}

func quota(cap, rate float64) policy.Quota {
	return policy.Quota{Capacity: cap, RatePerSec: rate}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func newLimiter(c *clock, cap, rate float64) *limiter.Limiter {
	l, err := limiter.New(quota(cap, rate), c.now)
	must(err)
	return l
}

func main() {
	c := &clock{t: time.Unix(1000, 0)}

	// 1. Both levels pass: each bucket is deducted.
	l1 := newLimiter(c, 20, 0)
	must(l1.Register("a", quota(10, 0)))
	must(l1.Allow("a", 4))
	s, _ := l1.Inspect("a")
	check("both levels deducted on pass", s.TenantBalance == 6 && s.GlobalBalance == 16)

	// 2. Global shortage: rejection leaves both balances untouched.
	must(l1.Register("b", quota(20, 0)))
	must(l1.Allow("b", 16)) // global now 0, tenant a still holds 6
	before, _ := l1.Inspect("a")
	err := l1.Allow("a", 5)
	after, _ := l1.Inspect("a")
	check("rejection changes no balance",
		errors.Is(err, limiter.ErrGlobalQuota) && before == after)

	// 3. Distinguishable rejection reasons; tenant wins when both short.
	l2 := newLimiter(c, 10, 0)
	must(l2.Register("a", quota(5, 0)))
	must(l2.Register("b", quota(10, 0)))
	must(l2.Allow("a", 5)) // tenant a empty
	must(l2.Allow("b", 5)) // global empty
	err = l2.Allow("a", 1)
	check("tenant-short error reported first", errors.Is(err, limiter.ErrTenantQuota))
	err = l2.Allow("ghost", 1)
	check("unknown tenant distinguishable", errors.Is(err, limiter.ErrTenantNotFound))

	// 4. Continuous refill and cap.
	l3 := newLimiter(c, 1000, 0)
	must(l3.Register("a", quota(10, 4)))
	must(l3.Allow("a", 10))
	c.advance(1500 * time.Millisecond) // +6 tokens
	s, _ = l3.Inspect("a")
	half := s.TenantBalance == 6
	c.advance(time.Hour) // far past capacity
	s, _ = l3.Inspect("a")
	check("continuous refill and cap", half && s.TenantBalance == 10)

	// 5. Clock rewind must not drain tokens.
	must(l3.Allow("a", 3))
	c.t = c.t.Add(-time.Hour)
	s, _ = l3.Inspect("a")
	check("clock rewind does not drain", s.TenantBalance == 7)
	c.t = c.t.Add(time.Hour) // restore

	// 6. Oversized single request rejected immediately.
	err = l3.Allow("a", 11)
	check("over-capacity request rejected", errors.Is(err, limiter.ErrExceedsBurst))

	// 7. Shrinking capacity truncates the balance.
	must(l3.SetQuota("a", quota(4, 0)))
	s, _ = l3.Inspect("a")
	check("shrink truncates balance", s.TenantBalance == 4)

	// 8. Tenant isolation and global exhaustion.
	l4 := newLimiter(c, 10, 0)
	must(l4.Register("x", quota(5, 0)))
	must(l4.Register("y", quota(10, 0)))
	must(l4.Allow("x", 5))              // x exhausted, global 5
	isolated := l4.Allow("y", 5) == nil // y unaffected; global now 0, y keeps 5
	allDenied := errors.Is(l4.Allow("x", 1), limiter.ErrTenantQuota) &&
		errors.Is(l4.Allow("y", 1), limiter.ErrGlobalQuota)
	check("tenant isolation and global floor", isolated && allDenied)

	// 9. Inspect is read-only and stable.
	s1, _ := l3.Inspect("a")
	s2, _ := l3.Inspect("a")
	check("inspect is read-only and stable", s1 == s2)

	// 10. Concurrency never oversells.
	check("concurrent no oversell", runConcurrent(c))

	fmt.Printf("TOTAL %d checks, %d failed\n", 12, failures)
	if failures > 0 {
		panic("demo failed")
	}
}

func runConcurrent(c *clock) bool {
	l := newLimiter(c, 200, 0)
	const tenants = 8
	for i := 0; i < tenants; i++ {
		if l.Register(string(rune('a'+i)), quota(50, 0)) != nil {
			return false
		}
	}
	var per [tenants]atomic.Int64
	var total atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < tenants; i++ {
		for j := 0; j < 8; j++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				name := string(rune('a' + idx))
				for k := 0; k < 100; k++ {
					if l.Allow(name, 1) == nil {
						per[idx].Add(1)
						total.Add(1)
					}
				}
			}(i)
		}
	}
	wg.Wait()
	for i := 0; i < tenants; i++ {
		if per[i].Load() > 50 {
			return false
		}
	}
	return total.Load() == 200
}
