// demo 逐条验证 retry 包的 8 条语义并打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/retry"
)

var failed bool

func check(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	// 1. 间隔数恒比尝试数少一：3 次全失败只等 2 次，最后一次失败后不等。
	sleeps := 0
	r := retry.New(retry.Policy{MaxAttempts: 3, Base: time.Millisecond},
		func(time.Duration) { sleeps++ }, nil)
	r.Do(func(int) error { return errors.New("x") })
	check("1 waits == attempts-1, no wait after last failure",
		len(r.Delays()) == 2 && sleeps == 2)

	// 2. 退避序列确定：Base*Factor^(k-1)， capped by Cap。
	r = retry.New(retry.Policy{MaxAttempts: 5, Base: 100 * time.Millisecond,
		Factor: 3, Cap: 250 * time.Millisecond}, func(time.Duration) {}, nil)
	r.Do(func(int) error { return errors.New("x") })
	want := []time.Duration{100, 250, 250, 250}
	got := r.Delays()
	ok := len(got) == len(want)
	for i := range want {
		ok = ok && got[i] == want[i]*time.Millisecond
	}
	check("2 deterministic backoff Base*Factor^(k-1) capped by Cap", ok)

	// 3. 抖动有界且每次等待只调用一次 rnd。
	seq := []float64{0, 0.5, 0.999}
	calls := 0
	r = retry.New(retry.Policy{MaxAttempts: 4, Base: 200 * time.Millisecond,
		JitterPct: 50}, func(time.Duration) {},
		func() float64 { v := seq[calls]; calls++; return v })
	r.Do(func(int) error { return errors.New("x") })
	d := r.Delays()
	check("3 jitter bounded, one rnd call per wait",
		calls == 3 && d[0] == 100*time.Millisecond &&
			d[1] == 200*time.Millisecond &&
			d[2] > 200*time.Millisecond && d[2] < 300*time.Millisecond)

	// 4. Permanent 错误立即中止，不再等也不再尝试。
	orig := errors.New("disk gone")
	fnCalls := 0
	r = retry.New(retry.Policy{MaxAttempts: 5, Base: time.Millisecond},
		func(time.Duration) {}, nil)
	n, err := r.Do(func(attempt int) error {
		fnCalls++
		if attempt == 2 {
			return retry.Permanent(orig)
		}
		return errors.New("transient")
	})
	check("4 permanent error aborts immediately",
		n == 2 && fnCalls == 2 && len(r.Delays()) == 1 &&
			errors.Is(err, retry.ErrAborted) && errors.Is(err, orig))

	// 5. 第 k 次成功即止。
	fnCalls = 0
	r = retry.New(retry.Policy{MaxAttempts: 5, Base: time.Millisecond},
		func(time.Duration) {}, nil)
	n, err = r.Do(func(int) error {
		fnCalls++
		if fnCalls == 3 {
			return nil
		}
		return errors.New("flaky")
	})
	check("5 success stops, returns k, delays k-1",
		err == nil && n == 3 && fnCalls == 3 && len(r.Delays()) == 2)

	// 6. 次数用尽：ErrExhausted 且能取到最后一次的原错误。
	first, last := errors.New("first"), errors.New("last")
	r = retry.New(retry.Policy{MaxAttempts: 3, Base: time.Millisecond},
		func(time.Duration) {}, nil)
	_, err = r.Do(func(attempt int) error {
		if attempt == 1 {
			return first
		}
		return last
	})
	check("6 exhausted wraps last error, not first",
		errors.Is(err, retry.ErrExhausted) && errors.Is(err, last) &&
			!errors.Is(err, first))

	// 7. attempt 从 1 连续递增；MaxAttempts<=0 至少跑一次。
	var attempts []int
	r = retry.New(retry.Policy{MaxAttempts: 4, Base: time.Millisecond},
		func(time.Duration) {}, nil)
	r.Do(func(a int) error { attempts = append(attempts, a); return errors.New("x") })
	ok = len(attempts) == 4
	for i, a := range attempts {
		ok = ok && a == i+1
	}
	r0 := retry.New(retry.Policy{MaxAttempts: 0}, func(time.Duration) {}, nil)
	n0, _ := r0.Do(func(int) error { return errors.New("x") })
	check("7 attempts numbered 1..n, MaxAttempts<=0 runs once", ok && n0 == 1)

	// 8. Runner 可复用且并发安全，各次 Do 的 Delays 不串台。
	r = retry.New(retry.Policy{MaxAttempts: 3, Base: time.Millisecond},
		func(time.Duration) {}, nil)
	r.Do(func(int) error { return errors.New("x") })
	r.Do(func(int) error { return nil })
	reused := len(r.Delays()) == 0
	var totalSleeps int64
	var mu sync.Mutex
	rc := retry.New(retry.Policy{MaxAttempts: 3, Base: time.Millisecond, Factor: 2},
		func(d time.Duration) { mu.Lock(); totalSleeps += int64(d); mu.Unlock() }, nil)
	var wg sync.WaitGroup
	clean := true
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				nn, e := rc.Do(func(int) error { return errors.New("x") })
				if nn != 3 || !errors.Is(e, retry.ErrExhausted) {
					clean = false
				}
			}
		}()
	}
	wg.Wait()
	wantSum := int64(8*5) * int64(3*time.Millisecond) // 每次 Do 等 1ms+2ms
	check("8 reusable and concurrency-safe, no cross-talk",
		reused && clean && totalSleeps == wantSum)

	if failed {
		fmt.Println("RESULT: FAIL")
	} else {
		fmt.Println("RESULT: all checks passed")
	}
	os.Exit(0)
}
