// Command demo checks every retry semantic and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/retry"
)

var errBoom = errors.New("boom")

func failAlways() func(int) error { return func(int) error { return errBoom } }

func newRunner(p retry.Policy) *retry.Runner {
	return retry.New(p, func(time.Duration) {}, nil)
}

func check1() bool { // waits == attempts-1
	r := newRunner(retry.Policy{MaxAttempts: 3, Base: time.Millisecond})
	r.Do(failAlways())
	return len(r.Delays()) == 2
}

func check2() bool { // deterministic Base*Factor^(k-1), capped
	r := newRunner(retry.Policy{MaxAttempts: 5, Base: 100 * time.Millisecond, Factor: 2, Cap: 250 * time.Millisecond})
	r.Do(failAlways())
	want := []time.Duration{100, 200, 250, 250}
	got := r.Delays()
	for i := range want {
		if got[i] != want[i]*time.Millisecond {
			return false
		}
	}
	return true
}

func check3() bool { // bounded jitter, one rnd draw per wait
	seq := []float64{0, 0.5, 0.999}
	draws := 0
	rnd := func() float64 { v := seq[draws]; draws++; return v }
	r := retry.New(retry.Policy{MaxAttempts: 4, Base: 200 * time.Millisecond, JitterPct: 50}, func(time.Duration) {}, rnd)
	r.Do(failAlways())
	d := r.Delays()
	return draws == 3 && d[0] == 100*time.Millisecond && d[1] == 200*time.Millisecond &&
		d[2] > 299*time.Millisecond && d[2] <= 300*time.Millisecond
}

func check4() bool { // permanent aborts immediately
	r := newRunner(retry.Policy{MaxAttempts: 5, Base: time.Second})
	calls := 0
	_, err := r.Do(func(int) error { calls++; return retry.Permanent(errBoom) })
	return calls == 1 && len(r.Delays()) == 0 &&
		errors.Is(err, retry.ErrAborted) && errors.Is(err, errBoom)
}

func check5() bool { // success on k stops, returns k, k-1 delays
	r := newRunner(retry.Policy{MaxAttempts: 5, Base: time.Millisecond})
	calls := 0
	attempt, err := r.Do(func(int) error {
		calls++
		if calls < 3 {
			return errBoom
		}
		return nil
	})
	return err == nil && attempt == 3 && calls == 3 && len(r.Delays()) == 2
}

func check6() bool { // exhaustion wraps ErrExhausted + last error
	errs := []error{errors.New("e1"), errors.New("e2"), errors.New("e3")}
	i := 0
	r := newRunner(retry.Policy{MaxAttempts: 3})
	_, err := r.Do(func(int) error { e := errs[i]; i++; return e })
	return errors.Is(err, retry.ErrExhausted) && errors.Is(err, errs[2]) && !errors.Is(err, errs[0])
}

func check7() bool { // attempts numbered 1..n; MaxAttempts<=0 runs once
	seen := []int{}
	newRunner(retry.Policy{MaxAttempts: 3}).Do(func(a int) error { seen = append(seen, a); return errBoom })
	calls := 0
	newRunner(retry.Policy{MaxAttempts: 0}).Do(func(int) error { calls++; return errBoom })
	return len(seen) == 3 && seen[0] == 1 && seen[1] == 2 && seen[2] == 3 && calls == 1
}

func check8() bool { // reuse resets; concurrent Do stays consistent
	r := newRunner(retry.Policy{MaxAttempts: 3, Base: 10 * time.Millisecond, Factor: 2})
	r.Do(failAlways())
	r.Do(func(int) error { return nil })
	if len(r.Delays()) != 0 {
		return false
	}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	var wg sync.WaitGroup
	bad := make(chan struct{}, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Do(failAlways())
			got := r.Delays()
			if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
				bad <- struct{}{}
			}
		}()
	}
	wg.Wait()
	return len(bad) == 0
}

func main() {
	checks := []struct {
		name string
		fn   func() bool
	}{
		{"1 waits == attempts-1", check1},
		{"2 deterministic backoff+cap", check2},
		{"3 bounded jitter, 1 rnd draw", check3},
		{"4 permanent aborts at once", check4},
		{"5 success stops, returns k", check5},
		{"6 exhausted wraps last err", check6},
		{"7 attempts numbered 1..n", check7},
		{"8 reuse + concurrent safe", check8},
	}
	failed := 0
	for _, c := range checks {
		verdict := "OK  "
		if !c.fn() {
			verdict = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", verdict, c.name)
	}
	fmt.Printf("summary: %d/%d semantics OK\n", len(checks)-failed, len(checks))
	if failed > 0 {
		os.Exit(1)
	}
}
