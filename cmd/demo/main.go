// Command demo exercises the least-connections load balancer and prints
// one OK/FAIL line per check. Exit code is non-zero if any check fails.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/lc"
	"ontology/svc"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

// countsString renders (c0,c1,c2) for the eight-step script.
func countsString(lb *api.LB) string {
	s := "("
	for i := 0; i < 3; i++ {
		if i > 0 {
			s += ","
		}
		c, _ := lb.Count(i)
		s += fmt.Sprint(c)
	}
	return s + ")"
}

func main() {
	// lc: Pick finds the minimum with tie-break to the smallest index.
	c := lc.New(3)
	c.Incr(0)
	c.Incr(2)
	pick1 := c.Pick() == 1
	c.Incr(1)
	pick2 := c.Pick() == 0 // all tied at 1 -> smallest index
	report("lc pick+tiebreak", pick1 && pick2)

	// lc: Pick examines a constant number of servers regardless of size.
	report("lc pick-cost constant", lc.VerifyPickCost())

	// svc: bad index and underflow are rejected with distinct errors,
	// and a rejected op leaves every count unchanged.
	m := svc.NewManager(2)
	ok := errors.Is(m.Acquire(-1), svc.ErrBadIndex) &&
		errors.Is(m.Acquire(2), svc.ErrBadIndex) &&
		errors.Is(m.Release(0), svc.ErrUnderflow) &&
		!errors.Is(svc.ErrBadIndex, svc.ErrUnderflow) &&
		!errors.Is(svc.ErrUnderflow, svc.ErrBadIndex)
	c0, _ := m.Count(0)
	c1, _ := m.Count(1)
	report("svc reject+no-trace", ok && c0 == 0 && c1 == 0)

	// api: the eight-step script from NOTES.md, step by step.
	lb, err := api.New(3)
	steps := err == nil
	steps = steps && lb.Acquire(0) == nil && countsString(lb) == "(1,0,0)"
	steps = steps && lb.Acquire(2) == nil && countsString(lb) == "(1,0,1)"
	steps = steps && lb.Pick() == 1
	steps = steps && lb.Acquire(1) == nil && countsString(lb) == "(1,1,1)"
	steps = steps && lb.Pick() == 0 // tie -> smallest index
	steps = steps && lb.Release(0) == nil && countsString(lb) == "(0,1,1)"
	steps = steps && lb.Pick() == 0
	steps = steps && errors.Is(lb.Release(0), api.ErrUnderflow) && countsString(lb) == "(0,1,1)"
	report("api eight-step script", steps)

	// api: three distinguishable failure kinds.
	_, cfgErr := api.New(0)
	three := errors.Is(cfgErr, api.ErrBadConfig) &&
		errors.Is(lb.Acquire(3), api.ErrBadIndex) &&
		errors.Is(lb.Release(0), api.ErrUnderflow) &&
		!errors.Is(api.ErrBadConfig, api.ErrBadIndex) &&
		!errors.Is(api.ErrBadIndex, api.ErrUnderflow) &&
		!errors.Is(api.ErrUnderflow, api.ErrBadConfig)
	report("api 3 distinct errors", three)

	// api: SelfCheck verifies all four invariants on fresh instances.
	report("api selfcheck invariants", lb.SelfCheck() == nil)

	// api: N goroutines each Acquire(0) once; final count must be N.
	const n = 500
	clb, _ := api.New(4)
	var wg sync.WaitGroup
	for k := 0; k < n; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = clb.Acquire(0)
			_ = clb.Pick()
		}()
	}
	wg.Wait()
	got, _ := clb.Count(0)
	report("api concurrent acquire", got == n)

	if failed {
		os.Exit(1)
	}
}
