// Command demo verifies each retry semantic and prints one OK/FAIL line per
// check. It always exits 0; failures are reported in the output.
package main

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/retry"
)

var errBoom = errors.New("boom")

func report(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
	}
	fmt.Printf("%s %s\n", mark, name)
}

func noSleep(time.Duration) {}

func fail(int) error { return errBoom }

func main() {
	// 1. waits = attempts - 1, never after the final failure.
	r1 := retry.New(retry.Policy{MaxAttempts: 3, Base: time.Second}, noSleep, nil)
	r1.Do(fail)
	report("1 waits == attempts-1 (2 delays for 3 attempts)", len(r1.Delays()) == 2)

	// 2. deterministic backoff with cap.
	r2 := retry.New(retry.Policy{MaxAttempts: 4, Base: 100, Factor: 3, Cap: 250}, noSleep, nil)
	r2.Do(fail)
	d := r2.Delays()
	report("2 deterministic backoff 100,250,250 (capped)",
		d[0] == 100 && d[1] == 250 && d[2] == 250)

	// 3. bounded jitter, one rnd draw per wait.
	seq, calls := []float64{0, 0.5, 0.999}, 0
	r3 := retry.New(retry.Policy{MaxAttempts: 4, Base: 1000, JitterPct: 20},
		noSleep, func() float64 { v := seq[calls]; calls++; return v })
	r3.Do(fail)
	d = r3.Delays()
	report("3 jitter bounded, rnd once per wait",
		calls == 3 && d[0] == 800 && d[1] == 1000 && d[2] == 1199)

	// 4. permanent error aborts immediately.
	slept, calls4 := 0, 0
	r4 := retry.New(retry.Policy{MaxAttempts: 5, Base: time.Second},
		func(time.Duration) { slept++ }, nil)
	_, err := r4.Do(func(int) error { calls4++; return retry.Permanent(errBoom) })
	report("4 permanent aborts, Is(ErrAborted) & Is(cause)",
		calls4 == 1 && slept == 0 && errors.Is(err, retry.ErrAborted) && errors.Is(err, errBoom))

	// 5. success stops at attempt k with k-1 delays.
	calls5 := 0
	r5 := retry.New(retry.Policy{MaxAttempts: 5, Base: time.Second}, noSleep, nil)
	n, err := r5.Do(func(int) error {
		calls5++
		if calls5 == 3 {
			return nil
		}
		return errBoom
	})
	report("5 success at attempt 3, 2 delays",
		err == nil && n == 3 && calls5 == 3 && len(r5.Delays()) == 2)

	// 6. exhaustion wraps ErrExhausted and the last error.
	errLast := errors.New("last")
	r6 := retry.New(retry.Policy{MaxAttempts: 3}, noSleep, nil)
	_, err = r6.Do(func(a int) error {
		if a < 3 {
			return errBoom
		}
		return errLast
	})
	report("6 exhausted wraps last error",
		errors.Is(err, retry.ErrExhausted) && errors.Is(err, errLast))

	// 7. attempts numbered 1..n; MaxAttempts<=0 runs once.
	seen := []int{}
	r7 := retry.New(retry.Policy{MaxAttempts: 3}, noSleep, nil)
	r7.Do(func(a int) error { seen = append(seen, a); return errBoom })
	once := retry.New(retry.Policy{MaxAttempts: 0}, noSleep, nil)
	runs := 0
	once.Do(func(int) error { runs++; return errBoom })
	report("7 attempts 1,2,3; MaxAttempts=0 runs once",
		len(seen) == 3 && seen[0] == 1 && seen[1] == 2 && seen[2] == 3 && runs == 1)

	// 8. reusable and concurrency-safe.
	r8 := retry.New(retry.Policy{MaxAttempts: 3, Base: 100, Factor: 2}, noSleep, nil)
	r8.Do(fail)
	r8.Do(fail)
	d = r8.Delays()
	ok := len(d) == 2 && d[0] == 100 && d[1] == 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r8.Do(fail)
			got := r8.Delays()
			mu.Lock()
			ok = ok && len(got) == 2 && got[0] == 100 && got[1] == 200
			mu.Unlock()
		}()
	}
	wg.Wait()
	report("8 reusable, race-safe, per-run delays", ok)
}
