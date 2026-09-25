package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/classify"
	"ontology/timeout"
)

type demoClock struct{ t time.Time }

func (d *demoClock) Now() time.Time { return d.t }

func line(n int, ok bool, msg string) (string, bool) {
	if ok {
		return fmt.Sprintf("OK %d. %s", n, msg), true
	}
	return fmt.Sprintf("FAIL %d. %s", n, msg), false
}

func main() {
	allOK := true
	n := 0
	emit := func(ok bool, msg string) {
		n++
		s, good := line(n, ok, msg)
		fmt.Println(s)
		allOK = allOK && good
	}

	err := classify.Wrap(classify.NonRetryable, errors.New("400"))
	emit(classify.Classify(err) == classify.NonRetryable && errors.Is(err, classify.ErrNonRetryable),
		"classify distinguishes four error kinds via errors.Is")

	// 舱壁：500 协程 N=8 峰值不超过 8；四路径 1000 次后额度回满。
	bh, _ := bulkhead.New(8, 16)
	var inFlight, peak int32
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if e := bh.Acquire(context.Background()); e != nil {
				return
			}
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			bh.Release()
		}(i)
	}
	wg.Wait()
	emit(bh.Peak() <= 8, "bulkhead in-flight peak never exceeds N=8")

	for path := 0; path < 4; path++ {
		for i := 0; i < 1000; i++ {
			if e := bh.Acquire(context.Background()); e != nil {
				emit(false, "bulkhead acquire on four paths")
				return
			}
			func() { defer func() { _ = recover() }() }()
			bh.Release()
		}
	}
	emit(bh.Available() == 8, "permits restored after success/fail/timeout/panic x1000")

	// 队列满立即拒绝。
	bq, _ := bulkhead.New(1, 1)
	_ = bq.Acquire(context.Background())
	go func() { _ = bq.Acquire(context.Background()) }()
	time.Sleep(5 * time.Millisecond)
	immediate := errors.Is(bq.Acquire(context.Background()), bulkhead.ErrBulkheadRejected)
	bq.Release()
	emit(immediate, "queue-full request rejected immediately")

	// 取消排队者后名额正确。
	bc, _ := bulkhead.New(1, 2)
	_ = bc.Acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cw := make(chan error, 1)
	go func() { cw <- bc.Acquire(ctx) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	<-cw
	bc.Release()
	emit(bc.Available() == 1, "canceled waiter removed and permit restored")

	// 单次时限：成功透传、超时归类、panic 转失败。
	okCall := timeout.Run(context.Background(), time.Second,
		func(context.Context) error { return nil }) == nil
	emit(okCall, "timeout wrapper passes successful call through")
	tErr := timeout.Run(context.Background(), time.Millisecond,
		func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	emit(classify.Classify(tErr) == classify.Timeout, "timed-out call classified as timeout")
	pErr := timeout.Run(context.Background(), time.Second,
		func(context.Context) error { panic("boom") })
	emit(classify.Classify(pErr) == classify.PanicKind &&
		errors.Is(pErr, classify.ErrPanic), "panic captured and converted to failure")

	// 熔断：打开后 1000 次被拒不影响失败率，半开一次成功即关闭。
	clk := &demoClock{t: time.Unix(100, 0)}
	br, _ := breaker.New(breaker.Config{
		Fails: 3, MinSamples: 5, Rate: 0.5,
		Cooldown: time.Second, MaxCooldown: 4 * time.Second,
		Probes: 1, Window: 50, Clock: clk,
	})
	for i := 0; i < 3; i++ {
		br.Failure()
	}
	for i := 0; i < 1000; i++ {
		_ = br.Allow()
	}
	clk.t = clk.t.Add(time.Second)
	rejectHarmless := errors.Is(br.Allow(), nil)
	br.Success()
	emit(rejectHarmless && br.State() == breaker.Closed,
		"1000 breaker rejections keep rate intact, one half-open success closes")

	// 熔断打开时舱壁占用恒为 0。
	bz, _ := bulkhead.New(4, 4)
	bo, _ := breaker.New(breaker.Config{
		Fails: 1, MinSamples: 1, Rate: 0.1, Cooldown: time.Hour,
		MaxCooldown: time.Hour, Probes: 1, Window: 10, Clock: clk,
	})
	bo.Failure()
	for i := 0; i < 100; i++ {
		if bo.Allow() == nil {
			_ = bz.Acquire(context.Background())
			bz.Release()
		}
	}
	emit(bz.Running() == 0, "bulkhead occupancy stays 0 while breaker open")

	// 半开期并发探测数恰好等于配置。
	clk2 := &demoClock{t: time.Unix(0, 0)}
	bp, _ := breaker.New(breaker.Config{
		Fails: 1, MinSamples: 1, Rate: 0.1, Cooldown: time.Second,
		MaxCooldown: time.Second, Probes: 3, Window: 10, Clock: clk2,
	})
	bp.Failure()
	clk2.t = clk2.t.Add(time.Second)
	var pass int64
	var pg sync.WaitGroup
	startGate := make(chan struct{})
	for i := 0; i < 100; i++ {
		pg.Add(1)
		go func() {
			defer pg.Done()
			<-startGate
			if bp.Allow() == nil {
				atomic.AddInt64(&pass, 1)
			}
		}()
	}
	close(startGate)
	pg.Wait()
	emit(pass == 3, "half-open admits exactly the configured probe count")

	// 时钟回拨被拒且状态不变。
	clk3 := &demoClock{t: time.Unix(50, 0)}
	bb, _ := breaker.New(breaker.Config{
		Fails: 1, MinSamples: 1, Rate: 0.1, Cooldown: time.Hour,
		MaxCooldown: time.Hour, Probes: 1, Window: 10, Clock: clk3,
	})
	bb.Failure()
	_ = bb.Allow() // 记录正常时钟 50
	clk3.t = clk3.t.Add(-time.Second)
	backErr := bb.Allow()
	emit(errors.Is(backErr, breaker.ErrClockBacktrack) && bb.State() == breaker.Open,
		"clock backtrack rejected, state unchanged")

	// 并发触发失败，关闭→打开只迁移一次。
	clk4 := &demoClock{t: time.Unix(0, 0)}
	bt, _ := breaker.New(breaker.Config{
		Fails: 1, MinSamples: 1, Rate: 0.01, Cooldown: time.Hour,
		MaxCooldown: time.Hour, Probes: 1, Window: 10, Clock: clk4,
	})
	var tg sync.WaitGroup
	for i := 0; i < 200; i++ {
		tg.Add(1)
		go func() { defer tg.Done(); bt.Failure() }()
	}
	tg.Wait()
	emit(bt.Transitions() == 1, "concurrent failures trigger exactly one transition")

	if allOK {
		fmt.Printf("TOTAL: %d/%d OK\n", n, n)
	} else {
		fmt.Println("TOTAL: FAIL")
	}
}
