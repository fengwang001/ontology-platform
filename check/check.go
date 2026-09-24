// Package check 提供顺序执行的朴素参照与 pmap.Run 的校验辅助。
package check

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"ontology/pmap"
)

// Naive 按下标顺序逐条执行 fn，遇错即停，全部成功时返回全部结果。
func Naive[In, Out any](ctx context.Context, inputs []In, fn func(context.Context, In) (Out, error)) ([]Out, error) {
	outs := make([]Out, 0, len(inputs))
	for _, in := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		outs = append(outs, out)
	}
	return outs, nil
}

type job struct {
	d   time.Duration
	v   int
	err error
}

// runJobs 执行一次 pmap.Run，返回 emit 的结果序列与 fn 并发峰值。
func runJobs(ctx context.Context, ins []job, n, w int) ([]int, int, error) {
	var got []int
	var cur, peak atomic.Int64
	err := pmap.Run(ctx, ins, n, w, func(ctx context.Context, j job) (int, error) {
		c := cur.Add(1)
		for p := peak.Load(); c > p && !peak.CompareAndSwap(p, c); p = peak.Load() {
		}
		defer cur.Add(-1)
		select {
		case <-time.After(j.d):
			return j.v, j.err
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}, func(_, out int) { got = append(got, out) })
	return got, int(peak.Load()), err
}

// goroutinesSettled 在宽限 d 内等待 goroutine 数回落到 base 以内。
func goroutinesSettled(base int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if runtime.NumGoroutine() <= base {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// run 包装 runJobs，并断言 fn 并发峰值不超过 n。
func run(t *testing.T, ctx context.Context, ins []job, n, w int) ([]int, error) {
	t.Helper()
	got, peak, err := runJobs(ctx, ins, n, w)
	if n > 0 && peak > n {
		t.Fatalf("fn 并发峰值 %d 超过 n=%d", peak, n)
	}
	return got, err
}
