// Command demo is a self-contained, fully deterministic walk-through of the
// multi-tenant token-bucket rate limiter. It never reads the wall clock and
// never sleeps: time is advanced via an injected fake clock. Run it with:
//
//	go run ./cmd/demo
package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology"
)

func main() {
	t0 := time.Unix(1_700_000_000, 0)
	pass, total := 0, 0
	check := func(name string, ok bool, detail string) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s %s\n", name, detail)
		} else {
			fmt.Printf("FAIL %s %s\n", name, detail)
		}
	}

	// 1. Fraction accumulation: ten 100ms steps must equal one 1s jump.
	l1, _ := ontology.NewLimiter(ontology.Config{Capacity: 100, Rate: 7, Shards: 4})
	_, _ = l1.Allow("x", 100, t0)
	now := t0
	for i := 0; i < 10; i++ {
		now = now.Add(100 * time.Millisecond)
		_, _ = l1.Available("x", now)
	}
	step, _ := l1.Available("x", now)
	l2, _ := ontology.NewLimiter(ontology.Config{Capacity: 100, Rate: 7, Shards: 4})
	_, _ = l2.Allow("x", 100, t0)
	jump, _ := l2.Available("x", t0.Add(time.Second))
	check("零头累积(100ms*10 == 1s*1)", step == 7 && jump == 7,
		fmt.Sprintf("分步=%d 一次=%d", step, jump))

	// 2. Refill never exceeds capacity, even after a huge time jump.
	l3, _ := ontology.NewLimiter(ontology.Config{Capacity: 5, Rate: 1, Shards: 4})
	_, _ = l3.Allow("x", 5, t0)
	full, _ := l3.Available("x", t0.Add(1000*time.Hour))
	check("补满后不超过容量", full == 5, fmt.Sprintf("可用=%d/5", full))

	// 3. A reversed timestamp is rejected and changes no state.
	l4, _ := ontology.NewLimiter(ontology.Config{Capacity: 5, Rate: 1, Shards: 4})
	_, _ = l4.Allow("x", 2, t0)
	_, errRev := l4.Allow("x", 1, t0.Add(-time.Second))
	after, _ := l4.Available("x", t0)
	check("时间倒流被拒绝且状态不变", errors.Is(errRev, ontology.ErrTimeReversed) && after == 3,
		fmt.Sprintf("err=%v 余量=%d", errRev, after))

	// 4. n = 0 / negative / over-capacity boundaries.
	l5, _ := ontology.NewLimiter(ontology.Config{Capacity: 5, Rate: 1, Shards: 4})
	okZero, _ := l5.Allow("x", 0, t0)
	_, errNeg := l5.Allow("x", -1, t0)
	okBig, errBig := l5.Allow("x", 6, t0)
	zeroFree, _ := l5.Available("x", t0)
	check("n=0放行/负数参数错/超容量永久拒绝",
		okZero && errors.Is(errNeg, ontology.ErrInvalidRequest) &&
			!okBig && errors.Is(errBig, ontology.ErrRequestExceedsCapacity) && zeroFree == 5,
		fmt.Sprintf("n=0=%v 负=%v 超量=%v", okZero, errNeg, errBig))

	// 5. Reported wait matches the real refill time, down to 1ns.
	setup := func() (*ontology.Limiter, *ontology.InsufficientTokensError) {
		l, _ := ontology.NewLimiter(ontology.Config{Capacity: 5, Rate: 2, Shards: 4})
		_, _ = l.Allow("x", 4, t0) // 1 token left
		_, e := l.Allow("x", 3, t0)
		var ite *ontology.InsufficientTokensError
		errors.As(e, &ite)
		return l, ite
	}
	_, info := setup()
	learly, _ := setup()
	earlyOk, earlyErr := learly.Allow("x", 3, t0.Add(info.RetryAfter-time.Nanosecond))
	lexact, _ := setup()
	exactOk, exactErr := lexact.Allow("x", 3, t0.Add(info.RetryAfter))
	check("等待时长与实际补满一致(±1ns)",
		info.RetryAfter == time.Second &&
			!earlyOk && errors.Is(earlyErr, ontology.ErrInsufficientTokens) &&
			exactOk && exactErr == nil,
		fmt.Sprintf("需等待=%v, 早1ns拒绝=%v, 准点放行=%v", info.RetryAfter, !earlyOk, exactOk))

	// 6. Two tenants never interfere.
	l7, _ := ontology.NewLimiter(ontology.Config{Capacity: 5, Rate: 1, Shards: 4})
	_, _ = l7.Allow("a", 5, t0)
	bOk, _ := l7.Allow("b", 5, t0)
	aLeft, _ := l7.Available("a", t0)
	bLeft, _ := l7.Available("b", t0)
	check("租户相互隔离", bOk && aLeft == 0 && bLeft == 0,
		fmt.Sprintf("a耗尽后 b放行=%v, 余量 a=%d b=%d", bOk, aLeft, bLeft))

	// 7. An idle, reclaimed tenant restarts with a full bucket.
	l8, _ := ontology.NewLimiter(ontology.Config{Capacity: 5, Rate: 1, Shards: 4})
	_, _ = l8.Allow("idle", 5, t0)
	activeBefore := l8.ActiveTenants()
	n := l8.ReclaimIdle(t0.Add(time.Nanosecond))
	reborn, _ := l8.Available("idle", t0.Add(time.Nanosecond))
	okFull, _ := l8.Allow("idle", 5, t0.Add(time.Nanosecond))
	check("不活跃租户回收后满桶重来", activeBefore == 1 && n == 1 && reborn == 5 && okFull,
		fmt.Sprintf("回收=%d 再来满桶=%v", n, okFull))

	// 8. Concurrency on one tenant never over-issues, with a frozen clock.
	l9, _ := ontology.NewLimiter(ontology.Config{Capacity: 200, Rate: 10, Shards: 8})
	var granted int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 500; i++ {
				ok, err := l9.Allow("hot", 1, t0)
				if err == nil && ok {
					atomic.AddInt64(&granted, 1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("并发同租户不超发", granted == 200,
		fmt.Sprintf("64 goroutine 共放行=%d/200", granted))

	fmt.Printf("总计 %d/%d 项通过\n", pass, total)
}
