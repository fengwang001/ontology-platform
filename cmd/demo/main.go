package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/bulkhead"
	"ontology/classify"
	"ontology/timeout"
)

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Println("OK  " + name)
		} else {
			fmt.Println("FAIL " + name)
		}
	}

	check("skeleton", true)
	check("error classes via errors.Is",
		errors.Is(classify.ErrRetryable, classify.ErrRetryable) &&
			!errors.Is(classify.ErrRetryable, classify.ErrFatal) &&
			classify.Of(classify.ErrTimeout) == classify.Timeout &&
			classify.Of(classify.ErrFatal) == classify.Fatal)

	// 舱壁：500 协程 / N=8，峰值不超过 8；四条路径后额度回满。
	bh8, _ := bulkhead.New(bulkhead.Config{Capacity: 8, Queue: 1000})
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := bh8.Acquire(context.Background())
			if err != nil {
				return
			}
			defer func() { _ = recover() }()
			time.Sleep(time.Millisecond)
			rel()
		}()
	}
	wg.Wait()
	check("bulkhead inflight peak <= N", bh8.Peak() <= 8 && bh8.Available() == 8)

	// 队列满立即拒绝（N=1,Q=0，占住唯一名额）。
	bh1, _ := bulkhead.New(bulkhead.Config{Capacity: 1})
	rel1, _ := bh1.Acquire(context.Background())
	start := time.Now()
	_, fullErr := bh1.Acquire(context.Background())
	rel1()
	check("queue full rejects immediately",
		errors.Is(fullErr, bulkhead.ErrBulkheadFull) && time.Since(start) < 5*time.Millisecond)

	// 四条路径各 1000 次，结束额度回满；下游 X 耗尽不影响 Y。
	pathOK := true
	for p := 0; p < 4; p++ {
		bh, _ := bulkhead.New(bulkhead.Config{Capacity: 4, Queue: 1000})
		var pwg sync.WaitGroup
		for i := 0; i < 1000; i++ {
			pwg.Add(1)
			go func() {
				defer pwg.Done()
				rel, err := bh.Acquire(context.Background())
				if err != nil {
					pathOK = false
					return
				}
				defer func() { _ = recover() }()
				if p == 3 {
					defer rel()
					panic("x")
				}
				rel()
			}()
		}
		pwg.Wait()
		if bh.Available() != 4 {
			pathOK = false
		}
	}
	check("slots restored on all 4 paths", pathOK)

	bx, _ := bulkhead.New(bulkhead.Config{Name: "X", Capacity: 1})
	by, _ := bulkhead.New(bulkhead.Config{Name: "Y", Capacity: 2})
	relx, _ := bx.Acquire(context.Background())
	yOK := 0
	for i := 0; i < 100; i++ {
		if rel, err := by.Acquire(context.Background()); err == nil {
			yOK++
			rel()
		}
	}
	relx()
	check("downstream X exhaustion does not affect Y", yOK == 100 && bx.Available() == 1)

	// timeout：超时归 Timeout；panic 被捕获成 Retryable，不击穿。
	tErr := timeout.Do(context.Background(), time.Millisecond,
		func(ctx context.Context) error { <-ctx.Done(); time.Sleep(5 * time.Millisecond); return nil })
	panicErr := timeout.Do(context.Background(), 50*time.Millisecond,
		func(context.Context) error { panic("boom") })
	check("timeout deadline and panic capture",
		errors.Is(tErr, timeout.ErrTimedOut) && classify.Of(tErr) == classify.Timeout &&
			errors.Is(panicErr, classify.ErrRetryable))

	if pass == total {
		fmt.Printf("TOTAL %d/%d PASS\n", pass, total)
	} else {
		fmt.Printf("TOTAL %d/%d FAIL\n", pass, total)
	}
}
