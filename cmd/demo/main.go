// Command demo 对有界重排窗口保序并行映射器做端到端判定。
package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"time"

	"ontology/check"
	"ontology/pmap"
)

var passed, total int

func judge(name string, ok bool) {
	total++
	line := "FAIL"
	if ok {
		line = "OK"
		passed++
	}
	fmt.Printf("%s %s\n", line, name)
}

func main() {
	ctx := context.Background()
	inputs := make([]int, 100)
	for i := range inputs {
		inputs[i] = i
	}
	double := func(_ context.Context, i int) (int, error) { return i * 2, nil }
	want, _ := check.Naive(ctx, inputs, double)
	var got []int
	err := pmap.Run(ctx, inputs, 4, 4, double, func(_ int, out int) { got = append(got, out) })
	judge("order-matches-naive", err == nil && fmt.Sprint(got) == fmt.Sprint(want))

	slow := func(_ context.Context, i int) (int, error) {
		time.Sleep(time.Duration(i*7919%50) * time.Microsecond)
		return i, nil
	}
	_ = pmap.Run(ctx, make([]int, 2000), 8, 4, slow, func(int, int) {})
	judge("window-max-le-4", pmap.LastMaxBuffered() <= 4)

	err3, err5 := errors.New("e3"), errors.New("e5")
	failed5, allow3 := make(chan struct{}), make(chan struct{})
	gated := func(_ context.Context, i int) (int, error) {
		switch i {
		case 3:
			<-allow3
			return 0, err3
		case 5:
			close(failed5)
			return 0, err5
		}
		return i, nil
	}
	var emitted []int
	done := make(chan error, 1)
	go func() {
		done <- pmap.Run(ctx, []int{0, 1, 2, 3, 4, 5}, 8, 8, gated, func(i int, _ int) { emitted = append(emitted, i) })
	}()
	<-failed5
	close(allow3)
	err = <-done
	judge("fail-min-index-3", errors.Is(err, err3) && !errors.Is(err, err5))
	judge("emit-stops-at-3", fmt.Sprint(emitted) == "[0 1 2]")

	err = pmap.Run(ctx, inputs, 0, 1, double, func(int, int) {})
	judge("bad-config", errors.Is(err, pmap.ErrBadConfig))

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	err = pmap.Run(cctx, inputs, 2, 2, double, func(int, int) {})
	judge("ctx-cancel", errors.Is(err, context.Canceled))

	var cancelled atomic.Int64
	boom := errors.New("boom")
	err = pmap.Run(ctx, inputs, 4, 8, func(c context.Context, i int) (int, error) {
		if i == 0 {
			return 0, boom
		}
		<-c.Done()
		cancelled.Add(1)
		return 0, c.Err()
	}, func(int, int) {})
	judge("inflight-cancelled", errors.Is(err, boom) && cancelled.Load() >= 1)

	base := runtime.NumGoroutine()
	_ = pmap.Run(ctx, inputs, 4, 4, double, func(int, int) {})
	for i := 0; i < 100 && runtime.NumGoroutine() > base; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	judge("goroutine-baseline", runtime.NumGoroutine() <= base)

	fmt.Printf("%s total %d/%d\n", map[bool]string{true: "OK", false: "FAIL"}[passed == total], passed, total)
}
