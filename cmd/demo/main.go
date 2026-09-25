// Command demo 逐条演示并判定熔断 + 舱壁 + 时限调用保护器的关键性质。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/classify"
	"ontology/stat"
	"ontology/timeout"
)

var statuses []string

func report(ok bool, name string) {
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, name)
	statuses = append(statuses, status)
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newBreaker(clk breaker.Clock, cf, probes int, base time.Duration) *breaker.Breaker {
	b, err := breaker.New(breaker.Config{
		ConsecutiveFailures: cf, MinSamples: 1_000_000, FailureRate: 1,
		HalfOpenProbes: probes, BaseCooldown: base, MaxCooldown: 8 * base,
	}, clk)
	if err != nil {
		panic(err)
	}
	return b
}

// guard 是编排器：统计 → 熔断 → 舱壁 → 时限 → 记账，顺序固定（DESIGN 第 0、2 节）。
func guard(brk *breaker.Breaker, bh *bulkhead.Bulkhead, st *stat.Stats,
	lim time.Duration, fn func() error) {
	st.Request()
	if err := brk.Allow(); err != nil {
		st.RejectByBreaker()
		return
	}
	if err := bh.Acquire(context.Background()); err != nil {
		st.RejectByBulkhead()
		return
	}
	defer bh.Release()
	err := timeout.Do(lim, fn)
	st.Record(err)
	brk.Record(err)
}

func holdForever(b *bulkhead.Bulkhead) {
	go func() {
		if b.Acquire(context.Background()) == nil {
			select {}
		}
	}()
	time.Sleep(2 * time.Millisecond)
}

func waitForever(b *bulkhead.Bulkhead) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Acquire(ctx) }()
	time.Sleep(2 * time.Millisecond)
	return cancel
}

func burst(n int, f func()) {
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() { defer wg.Done(); f() }()
	}
	wg.Wait()
}

func checkClassify() bool {
	return classify.Classify(nil) == classify.KindNone &&
		classify.Classify(fmt.Errorf("w: %w", classify.ErrTimeout)) == classify.KindTimeout &&
		classify.IsBreakerFailure(classify.ErrRetryable) &&
		!classify.IsBreakerFailure(classify.ErrNonRetryable)
}

func checkTimeoutPanic() bool {
	slow := timeout.Do(time.Millisecond, func() error { time.Sleep(time.Second); return nil })
	panicked := timeout.Do(time.Second, func() error { panic("boom") })
	ok := timeout.Do(time.Second, func() error { return nil })
	return errors.Is(slow, classify.ErrTimeout) && errors.Is(panicked, classify.ErrPanic) && ok == nil
}

func main() {
	report(checkClassify(), "classify: four kinds, errors.Is, non-retryable not fed to breaker")
	report(checkPeak(), "bulkhead: in-flight peak <= N (500 goroutines, N=8)")
	report(checkQueueFull(), "bulkhead: N+Q+1th request rejected immediately, clock untouched")
	report(checkIsolation(), "bulkhead: downstream X exhausted does not affect Y")
	report(checkCancelSlot(), "bulkhead: cancelled waiter frees its queue slot")
	report(checkTimeoutPanic(), "timeout: limit enforced, panic captured, wrapper survives")
	report(checkRejectedNotCounted(), "breaker: 1000 rejections do not change failure stats")
	report(checkHalfOpenOneSuccess(), "breaker: half-open returns to closed after one probe success")
	report(checkOpenBulkheadIdle(), "order: open breaker keeps bulkhead occupancy 0")
	report(checkReleaseFourPaths(), "bulkhead: permits refill on success/fail/timeout/panic x1000")
	report(checkHalfOpenProbes(), "breaker: exactly HalfOpenProbes of 100 concurrent calls pass")
	report(checkClockRewind(), "breaker: clock rewind rejected with ErrClockRegression")
	report(checkMigrationOnce(), "breaker: concurrent failures trigger one migration, one backoff")
	report(checkStatEquations(), "stat: all three accounting equations hold after random traffic")

	failed := 0
	for _, s := range statuses {
		if s == "FAIL" {
			failed++
		}
	}
	fmt.Printf("TOTAL %d/%d OK\n", len(statuses)-failed, len(statuses))
	if failed > 0 {
		os.Exit(1)
	}
}
