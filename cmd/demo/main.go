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
	base := time.Unix(0, 0)
	passed := 0
	checks := 0

	report := func(name string, ok bool, detail string) {
		checks++
		if ok {
			passed++
			fmt.Printf("OK %s %s\n", name, detail)
			return
		}
		fmt.Printf("FAIL %s %s\n", name, detail)
	}

	limiter := ontology.NewLimiter(100, 7)
	_, _ = limiter.Allow("fraction", 100, base)
	stepped := limiter
	for i := 0; i < 10; i++ {
		now := base.Add(time.Duration(i+1) * 100 * time.Millisecond)
		_, _ = stepped.Allow("fraction", 0, now)
	}
	steppedTokens, _ := stepped.Available("fraction", base.Add(time.Second))

	oneShot := ontology.NewLimiter(100, 7)
	_, _ = oneShot.Allow("fraction", 100, base)
	oneShotTokens, _ := oneShot.Available("fraction", base.Add(time.Second))
	report("fractional-accumulation", steppedTokens == 7 && oneShotTokens == steppedTokens,
		fmt.Sprintf("10x100ms=%d one-shot=%d", steppedTokens, oneShotTokens))

	_, _ = limiter.Allow("capacity", 100, base)
	capped, _ := limiter.Available("capacity", base.Add(time.Hour))
	report("capacity-cap", capped == 100, fmt.Sprintf("available=%d", capped))

	_, _ = limiter.Allow("reverse", 50, base.Add(2*time.Hour))
	_, reversedErr := limiter.Allow("reverse", 1, base.Add(time.Hour))
	report("time-reversal", errors.Is(reversedErr, ontology.ErrTimeReversed), fmt.Sprint(reversedErr))

	zeroOK, zeroErr := limiter.Allow("bounds", 0, base.Add(3*time.Hour))
	_, negativeErr := limiter.Allow("bounds", -1, base.Add(3*time.Hour))
	_, tooLargeErr := limiter.Allow("bounds", 101, base.Add(3*time.Hour))
	report("n-zero-negative-oversized",
		zeroOK && zeroErr == nil && errors.Is(negativeErr, ontology.ErrInvalidRequest) &&
			errors.Is(tooLargeErr, ontology.ErrRequestTooLarge),
		fmt.Sprintf("n0=%v negative=%v oversized=%v", zeroErr, negativeErr, tooLargeErr))

	waiting := ontology.NewLimiter(10, 7)
	_, _ = waiting.Allow("wait", 10, base)
	_, waitErr := waiting.Allow("wait", 10, base)
	var insufficient *ontology.InsufficientError
	errors.As(waitErr, &insufficient)
	retryAt := base.Add(insufficient.RetryAfter)
	early, _ := waiting.Allow("wait", 10, retryAt.Add(-time.Nanosecond))
	exact, exactErr := waiting.Allow("wait", 10, retryAt)
	report("retry-after-accuracy",
		insufficient != nil && !early && exact && exactErr == nil,
		fmt.Sprintf("wait=%s early=%v exact=%v", insufficient.RetryAfter, early, exact))

	isolated := ontology.NewLimiter(5, 5)
	_, _ = isolated.Allow("a", 5, base)
	a, _ := isolated.Available("a", base)
	b, _ := isolated.Available("b", base)
	report("tenant-isolation", a == 0 && b == 5, fmt.Sprintf("a=%d b=%d", a, b))

	evictable := ontology.NewLimiter(10, 10)
	_, _ = evictable.Allow("idle", 10, base)
	before, _ := evictable.Available("idle", base)
	removed := evictable.EvictIdle(time.Nanosecond, base.Add(time.Nanosecond))
	after, _ := evictable.Available("idle", base.Add(time.Nanosecond))
	report("idle-eviction-resets", removed == 1 && before == 0 && after == 10,
		fmt.Sprintf("removed=%d before=%d after=%d", removed, before, after))

	concurrent := ontology.NewLimiter(50, 1_000_000_000)
	var granted atomic.Int64
	var ready, start, done sync.WaitGroup
	ready.Add(500)
	start.Add(1)
	done.Add(500)
	for i := 0; i < 500; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			start.Wait()
			if ok, _ := concurrent.Allow("same", 1, base.Add(2*time.Hour)); ok {
				granted.Add(1)
			}
		}()
	}
	ready.Wait()
	start.Done()
	done.Wait()
	report("same-tenant-no-overissue", granted.Load() == 50,
		fmt.Sprintf("granted=%d capacity=50", granted.Load()))

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, checks)
}
