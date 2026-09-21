// Command demo checks every documented semantic of the backpressure
// package and prints one OK/FAIL line per item. It always exits 0.
package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/backpressure"
)

var failures int

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%-44s %s\n", name, verdict)
}

func main() {
	// 1. Hysteresis: pause at >= high, resume only at <= low.
	c, _ := backpressure.New(10, 20, 100)
	c.Add(19)
	before := c.Stat().State
	c.Add(1) // 20 -> pause
	c.Sub(5)
	c.Add(4)
	c.Sub(8) // oscillate inside (low, high): 15,19,11
	mid := c.Stat()
	c.Sub(0) // invalid, no-op
	c.Sub(1) // 11-1 = 10 == low -> resume
	check("1 hysteresis: pause>=high, resume<=low only",
		before == backpressure.Flowing &&
			mid.State == backpressure.Paused && mid.Pauses == 1 && mid.Resumes == 0 &&
			c.Stat().State == backpressure.Flowing && c.Stat().Resumes == 1)

	// 2. Callbacks: exactly once per transition, registration order.
	c2, _ := backpressure.New(5, 10, 100)
	var log []string
	c2.OnChange(func(s backpressure.State) { _ = c2.Stat(); log = append(log, "a"+fmt.Sprint(int(s))) })
	c2.OnChange(func(s backpressure.State) { log = append(log, "b"+fmt.Sprint(int(s))) })
	c2.Add(3)
	c2.Add(7) // pause
	c2.Sub(5) // resume
	check("2 callbacks: once per transition, in order",
		fmt.Sprint(log) == "[a1 b1 a0 b0]")

	// 3. Hard limit: whole amount rejected, exact-hard accepted.
	c3, _ := backpressure.New(10, 20, 50)
	c3.Add(40)
	rejected := !c3.Add(11)
	r3 := c3.Stat()
	exact := c3.Add(10)
	check("3 hard limit: reject whole, accept exact",
		rejected && r3.Level == 40 && r3.Rejected == 11 && exact && c3.Stat().Level == 50)

	// 4. Counters: only real transitions; rejection changes nothing.
	c4, _ := backpressure.New(10, 20, 50)
	c4.Add(25) // pause
	b4 := c4.Stat()
	c4.Add(26) // rejected
	r4 := c4.Stat()
	check("4 counters: rejection keeps state/counters",
		r4.State == b4.State && r4.Pauses == b4.Pauses && r4.Resumes == b4.Resumes &&
			r4.Pauses == 1 && r4.Resumes == 0)

	// 5. Sub clamps at 0; resume judged by clamped value.
	c5, _ := backpressure.New(0, 10, 100)
	c5.Add(10) // pause
	c5.Sub(50) // clamp to 0 -> resume
	r5 := c5.Stat()
	check("5 sub clamps at 0, resume on clamped value",
		r5.Level == 0 && r5.State == backpressure.Flowing && r5.Resumes == 1)

	// 6. Non-positive n is invalid for Add and Sub.
	c6, _ := backpressure.New(5, 10, 100)
	c6.Add(7)
	b6 := c6.Stat()
	invalid := !c6.Add(0) && !c6.Add(-3)
	c6.Sub(0)
	c6.Sub(-2)
	check("6 non-positive n invalid, no side effects",
		invalid && c6.Stat() == b6)

	// 7. Config validation.
	_, e1 := backpressure.New(5, 5, 10)
	_, e2 := backpressure.New(1, 11, 10)
	_, e3 := backpressure.New(-1, 5, 10)
	c7, e4 := backpressure.New(0, 1, 1)
	check("7 validation: bad configs rejected, low==0 ok",
		errors.Is(e1, backpressure.ErrBadWatermark) &&
			errors.Is(e2, backpressure.ErrBadWatermark) &&
			errors.Is(e3, backpressure.ErrBadWatermark) && e4 == nil && c7 != nil)

	// 8. Concurrency: conservation and counter/state consistency.
	c8, _ := backpressure.New(100, 500, 100000)
	var accepted, subbed atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if c8.Add(3) {
					accepted.Add(3)
					c8.Sub(3)
					subbed.Add(3)
				}
				_ = c8.Stat()
			}
		}()
	}
	wg.Wait()
	r8 := c8.Stat()
	consistent := (r8.State == backpressure.Paused && r8.Pauses == r8.Resumes+1) ||
		(r8.State == backpressure.Flowing && r8.Pauses == r8.Resumes)
	check("8 concurrent: level conserved, counters coherent",
		r8.Level == accepted.Load()-subbed.Load() && r8.Level >= 0 && consistent)

	fmt.Printf("summary: %d of 8 semantics failed\n", failures)
}
