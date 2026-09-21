// Command demo checks each semantic of the backpressure controller
// and prints one OK/FAIL line per semantic.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/backpressure"
)

var failures int

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%-28s %s\n", name, verdict)
}

func main() {
	// 1. Hysteresis: pause at >= high, resume only at <= low.
	c, _ := backpressure.New(10, 20, 100)
	c.Add(19)
	s1 := c.Stat().State == backpressure.Flowing
	c.Add(1) // 20 -> Paused
	s1 = s1 && c.Stat().State == backpressure.Paused
	c.Sub(9) // 11 > low, stay paused
	s1 = s1 && c.Stat().State == backpressure.Paused
	c.Sub(1) // 10 <= low -> Flowing
	s1 = s1 && c.Stat().State == backpressure.Flowing
	check("1 hysteresis", s1)

	// 2. Callbacks fire exactly once per real transition, in order.
	c, _ = backpressure.New(2, 4, 10)
	var log []string
	c.OnChange(func(s backpressure.State) { log = append(log, "a"+fmt.Sprint(int(s))) })
	c.OnChange(func(s backpressure.State) {
		_ = c.Stat() // must not deadlock
		log = append(log, "b"+fmt.Sprint(int(s)))
	})
	c.Add(4) // pause
	c.Add(1) // no transition
	c.Sub(3) // resume
	c.Sub(1) // no transition
	check("2 callbacks exactly once", fmt.Sprint(log) == "[a1 b1 a0 b0]")

	// 3. Hard limit rejects the whole amount; exact hard accepted.
	c, _ = backpressure.New(2, 4, 10)
	c.Add(8)
	r := c.Stat()
	s3 := c.Add(3) == false && r.Level == 8 && r.Rejected == 0
	r = c.Stat()
	s3 = s3 && r.Level == 8 && r.Rejected == 3
	s3 = s3 && c.Add(2) && c.Stat().Level == 10 // exactly hard
	check("3 hard limit", s3)

	// 4. Pauses/Resumes count only real transitions.
	c, _ = backpressure.New(2, 4, 5)
	c.Add(4) // pause
	c.Add(9) // rejected, no transition
	r = c.Stat()
	check("4 counters real only", r.Pauses == 1 && r.Resumes == 0 && r.Rejected == 9)

	// 5. Sub clamps at zero and resumes on the clamped value.
	c, _ = backpressure.New(4, 8, 10)
	c.Add(9)
	c.Sub(100)
	r = c.Stat()
	check("5 clamp at zero", r.Level == 0 && r.State == backpressure.Flowing)

	// 6. Zero/negative n is invalid and changes nothing.
	c, _ = backpressure.New(2, 4, 10)
	c.Add(5)
	before := c.Stat()
	s6 := !c.Add(0) && !c.Add(-3)
	c.Sub(0)
	c.Sub(-3)
	check("6 non-positive n", s6 && c.Stat() == before)

	// 7. Invalid configs return ErrBadWatermark and nil controller.
	s7 := true
	for _, w := range [][3]int64{{5, 5, 9}, {1, 11, 10}, {-1, 5, 9}} {
		cc, err := backpressure.New(w[0], w[1], w[2])
		s7 = s7 && errors.Is(err, backpressure.ErrBadWatermark) && cc == nil
	}
	_, err := backpressure.New(0, 1, 1)
	check("7 config validation", s7 && err == nil)

	// 8. Concurrent Add/Sub/Stat keeps counters self-consistent.
	c, _ = backpressure.New(50, 100, 200)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				if (i+g)%2 == 0 {
					c.Add(int64(g + 1))
				} else {
					c.Sub(int64(g + 1))
				}
				_ = c.Stat()
			}
		}(g)
	}
	wg.Wait()
	r = c.Stat()
	s8 := r.Level >= 0 && r.Level <= 200
	if r.State == backpressure.Paused {
		s8 = s8 && r.Pauses == r.Resumes+1
	} else {
		s8 = s8 && r.Pauses == r.Resumes
	}
	check("8 concurrency invariants", s8)

	if failures > 0 {
		fmt.Printf("%d check(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Println("all 8 semantics OK")
}
