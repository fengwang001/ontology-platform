// Command demo exercises every documented semantic of the backpressure
// package and prints one OK/FAIL line per rule.
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/backpressure"
)

var failed bool

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failed = true
	}
	fmt.Printf("%-34s %s\n", name, verdict)
}

func main() {
	c, _ := backpressure.New(10, 20, 30)

	// 1. Hysteresis: pause at >= high, resume only at <= low.
	c.Add(15)
	midFlow := c.Stat().State == backpressure.Flowing
	c.Add(5)
	pausedAtHigh := c.Stat().State == backpressure.Paused
	c.Sub(5)
	stillPaused := c.Stat().State == backpressure.Paused
	c.Sub(5)
	resumedAtLow := c.Stat().State == backpressure.Flowing
	check("1 hysteresis band", midFlow && pausedAtHigh && stillPaused && resumedAtLow)

	// 2. Callbacks fire exactly once per transition, in order.
	c2, _ := backpressure.New(5, 10, 50)
	var seq []backpressure.State
	c2.OnChange(func(s backpressure.State) { seq = append(seq, s); _ = c2.Stat() })
	c2.OnChange(func(s backpressure.State) { seq = append(seq, s) })
	c2.Add(4)
	c2.Add(6) // pause
	c2.Sub(5) // resume
	check("2 callbacks once, in order", len(seq) == 4 &&
		seq[0] == backpressure.Paused && seq[1] == backpressure.Paused &&
		seq[2] == backpressure.Flowing && seq[3] == backpressure.Flowing)

	// 3. Hard limit: wholesale rejection, exact fit accepted.
	c3, _ := backpressure.New(10, 20, 30)
	exactFit := c3.Add(30)
	overRejected := !c3.Add(1)
	r3 := c3.Stat()
	check("3 hard limit, no partial", exactFit && overRejected &&
		r3.Level == 30 && r3.Rejected == 1)

	// 4. Rejected Add changes no state and no counters.
	c4, _ := backpressure.New(10, 20, 25)
	c4.Add(15)
	c4.Add(20) // rejected
	r4 := c4.Stat()
	check("4 rejection keeps counters", r4.Level == 15 &&
		r4.State == backpressure.Flowing && r4.Pauses == 0 && r4.Resumes == 0)

	// 5. Sub clamps at zero and resumes on the clamped value.
	c5, _ := backpressure.New(10, 20, 100)
	c5.Add(25)
	c5.Sub(40)
	r5 := c5.Stat()
	check("5 sub clamps at zero", r5.Level == 0 &&
		r5.State == backpressure.Flowing && r5.Resumes == 1)

	// 6. Zero/negative amounts are invalid no-ops.
	c6, _ := backpressure.New(10, 20, 100)
	noop := !c6.Add(0) && !c6.Add(-3)
	c6.Sub(0)
	c6.Sub(-7)
	r6 := c6.Stat()
	check("6 non-positive n ignored", noop && r6.Level == 0 &&
		r6.Rejected == 0 && r6.Pauses == 0 && r6.Resumes == 0)

	// 7. Config validation: bad watermarks fail, low == 0 is legal.
	_, e1 := backpressure.New(10, 10, 100)
	_, e2 := backpressure.New(0, 30, 20)
	_, e3 := backpressure.New(-1, 10, 100)
	_, e4 := backpressure.New(0, 10, 10)
	check("7 watermark validation", e1 == backpressure.ErrBadWatermark &&
		e2 == backpressure.ErrBadWatermark && e3 == backpressure.ErrBadWatermark &&
		e4 == nil)

	// 8. Concurrent Add/Sub/Stat keeps the books consistent.
	c8, _ := backpressure.New(8, 16, 64)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if (w+i)%2 == 0 {
					c8.Add(1)
				} else {
					c8.Sub(1)
				}
				_ = c8.Stat()
			}
		}(w)
	}
	wg.Wait()
	r8 := c8.Stat()
	balanced := r8.Level >= 0 && r8.Level <= 64 &&
		(r8.State == backpressure.Paused) == (r8.Pauses == r8.Resumes+1)
	check("8 concurrent conservation", balanced)

	if failed {
		os.Exit(1)
	}
}
