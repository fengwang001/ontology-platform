package main

import (
	"context"
	"errors"
	"math/rand"
	"sync/atomic"
	"time"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/classify"
	"ontology/stat"
)

func failCall() error { return classify.ErrRetryable }

func checkPeak() bool {
	b, _ := bulkhead.New(8, 500)
	burst(500, func() {
		if b.Acquire(context.Background()) == nil {
			time.Sleep(time.Millisecond)
			b.Release()
		}
	})
	return b.Peak() == 8 && b.Available() == 8
}

func checkQueueFull() bool {
	b, _ := bulkhead.New(1, 1)
	holdForever(b)
	waitForever(b)
	clk := &fakeClock{now: time.Unix(0, 0)}
	before := clk.Now()
	err := b.Acquire(context.Background())
	return errors.Is(err, bulkhead.ErrBulkheadFull) && clk.Now().Equal(before)
}

func checkIsolation() bool {
	x, _ := bulkhead.New(1, 1)
	y, _ := bulkhead.New(1, 1)
	holdForever(x)
	waitForever(x)
	if y.Acquire(context.Background()) != nil {
		return false
	}
	y.Release()
	return y.Available() == 1
}

func checkCancelSlot() bool {
	b, _ := bulkhead.New(1, 1)
	holdForever(b)
	cancel := waitForever(b)
	if err := b.Acquire(context.Background()); !errors.Is(err, bulkhead.ErrBulkheadFull) {
		return false
	}
	cancel()
	time.Sleep(2 * time.Millisecond)
	joined := make(chan struct{})
	go func() {
		err := b.Acquire(context.Background())
		close(joined)
		_ = err
	}()
	select {
	case <-joined: // 说明又被立即拒绝（名额未归还）
		return false
	case <-time.After(10 * time.Millisecond):
		return true // 成功进入队列等待
	}
}

func checkRejectedNotCounted() bool {
	clk := &fakeClock{now: time.Unix(0, 0)}
	brk := newBreaker(clk, 2, 1, time.Second)
	bh, _ := bulkhead.New(4, 4)
	st := &stat.Stats{}
	guard(brk, bh, st, 0, failCall)
	guard(brk, bh, st, 0, failCall) // 连续 2 次失败 → 打开
	for i := 0; i < 1000; i++ {
		guard(brk, bh, st, 0, failCall) // 全部被熔断拒绝
	}
	s := st.Snapshot()
	return s.RejectedByBreaker == 1000 && s.Real == 2 && s.Failed() == 2 && s.SelfConsistent()
}

func checkHalfOpenOneSuccess() bool {
	clk := &fakeClock{now: time.Unix(0, 0)}
	brk := newBreaker(clk, 1, 1, time.Second)
	bh, _ := bulkhead.New(2, 2)
	st := &stat.Stats{}
	guard(brk, bh, st, 0, failCall)
	clk.Advance(time.Second)
	guard(brk, bh, st, 0, func() error { return nil })
	return brk.State() == breaker.Closed
}

func checkOpenBulkheadIdle() bool {
	clk := &fakeClock{now: time.Unix(0, 0)}
	brk := newBreaker(clk, 2, 1, time.Second)
	bhOpen, _ := bulkhead.New(4, 4)
	bhIdle, _ := bulkhead.New(4, 4)
	st := &stat.Stats{}
	guard(brk, bhOpen, st, 0, failCall)
	guard(brk, bhOpen, st, 0, failCall)
	for i := 0; i < 1000; i++ {
		guard(brk, bhIdle, st, 0, failCall)
	}
	return bhIdle.Peak() == 0 && bhIdle.Available() == 4
}

func checkReleaseFourPaths() bool {
	clk := &fakeClock{now: time.Unix(0, 0)}
	brk := newBreaker(clk, 1_000_000, 1, time.Hour)
	bh, _ := bulkhead.New(8, 64)
	st := &stat.Stats{}
	calls := []func() error{
		func() error { return nil },
		failCall,
		func() error { time.Sleep(50 * time.Millisecond); return nil }, // 1ms 超时
		func() error { panic("boom") },
	}
	for _, fn := range calls {
		for i := 0; i < 1000; i++ {
			guard(brk, bh, st, time.Millisecond, fn)
		}
	}
	return bh.Available() == 8 && bh.Peak() <= 8 && st.Snapshot().SelfConsistent()
}

func checkHalfOpenProbes() bool {
	clk := &fakeClock{now: time.Unix(0, 0)}
	brk := newBreaker(clk, 1, 3, time.Second)
	bh, _ := bulkhead.New(32, 32)
	st := &stat.Stats{}
	guard(brk, bh, st, 0, failCall)
	clk.Advance(time.Second)
	var passed atomic.Int64
	burst(100, func() {
		if brk.Allow() == nil {
			passed.Add(1)
		}
	})
	return passed.Load() == 3 && brk.State() == breaker.HalfOpen
}

func checkClockRewind() bool {
	clk := &fakeClock{now: time.Unix(10, 0)}
	brk := newBreaker(clk, 1, 1, time.Second)
	bh, _ := bulkhead.New(1, 1)
	st := &stat.Stats{}
	guard(brk, bh, st, 0, failCall)
	clk.Advance(-time.Second)
	return errors.Is(brk.Allow(), breaker.ErrClockRegression) && brk.State() == breaker.Open
}

func checkMigrationOnce() bool {
	clk := &fakeClock{now: time.Unix(0, 0)}
	brk, err := breaker.New(breaker.Config{
		ConsecutiveFailures: 1, MinSamples: 1, FailureRate: 1,
		HalfOpenProbes: 1, BaseCooldown: time.Second, MaxCooldown: 8 * time.Second,
	}, clk)
	if err != nil {
		return false
	}
	burst(100, func() { brk.Record(classify.ErrRetryable) })
	return brk.State() == breaker.Open && brk.Transitions() == 1 &&
		brk.Cooldown() == time.Second
}

func checkStatEquations() bool {
	clk := &fakeClock{now: time.Unix(0, 0)}
	brk := newBreaker(clk, 1_000_000, 1, time.Hour)
	bh, _ := bulkhead.New(4, 4)
	st := &stat.Stats{}
	rng := rand.New(rand.NewSource(1))
	burst(16, func() {
		for i := 0; i < 125; i++ {
			fn := failCall
			switch rng.Intn(4) {
			case 0:
				fn = func() error { return nil }
			case 1:
				fn = func() error { time.Sleep(time.Millisecond); return nil }
			case 2:
				fn = func() error { return classify.ErrNonRetryable }
			}
			guard(brk, bh, st, 5*time.Millisecond, fn)
		}
	})
	brk2 := newBreaker(clk, 1, 1, time.Hour)
	guard(brk2, bh, st, 0, failCall)
	for i := 0; i < 50; i++ {
		guard(brk2, bh, st, 0, failCall)
	}
	s := st.Snapshot()
	return s.SelfConsistent() && s.RejectedByBulkhead > 0 && s.RejectedByBreaker > 0
}
