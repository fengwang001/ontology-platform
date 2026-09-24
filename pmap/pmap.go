// Package pmap 有界重排窗口的保序并行映射：N 个 worker 并行处理，
// 下游按输入顺序收到结果，任意时刻暂存的乱序结果不超过 W 条。
package pmap

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"ontology/window"
)

// ErrBadConfig 表示 n < 1 或 w < 1。
var ErrBadConfig = errors.New("pmap: n 和 w 必须 >= 1")

var lastMax atomic.Int64

// LastMaxBuffered 返回最近一次 Run 的历史最大暂存数（供测试与 demo 判定）。
func LastMaxBuffered() int { return int(lastMax.Load()) }

// Run 并行处理 inputs 并按输入下标顺序回调 emit。
// 失败时返回最小失败下标的包装错误，emit 恰好覆盖失败点之前的下标。
func Run[In, Out any](ctx context.Context, inputs []In, n, w int, fn func(context.Context, In) (Out, error), emit func(i int, out Out)) error {
	if n < 1 || w < 1 {
		return ErrBadConfig
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		i   int
		out Out
		err error
	}
	results := make(chan result)
	win := window.New[Out]()
	started, inflight := 0, 0
	failIdx, failErr := len(inputs), error(nil)
	for inflight > 0 || (started < len(inputs) && failErr == nil && ctx.Err() == nil) {
		// 派发闸门：并发 <= n 且 已派发-已输出 < w（故暂存数 <= w）。
		for started < len(inputs) && inflight < n && started-win.Next() < w && failErr == nil && ctx.Err() == nil {
			i := started
			go func() {
				out, err := fn(ctx, inputs[i])
				results <- result{i, out, err}
			}()
			started++
			inflight++
		}
		r := <-results
		inflight--
		switch {
		case r.err == nil && r.i < failIdx:
			win.Put(r.i, r.out)
		case r.err == nil || (ctx.Err() != nil && errors.Is(r.err, ctx.Err())):
			// 失败点之后的成功、或被取消的任务：不暂存、不记为失败。
		case r.i < failIdx: // 真实失败，只保留下标最小者
			failIdx, failErr = r.i, r.err
			cancel()
		}
		start, ready := win.PopReady()
		for j, out := range ready {
			emit(start+j, out)
		}
	}
	lastMax.Store(int64(win.Max()))
	if failErr != nil {
		return fmt.Errorf("pmap: 输入 %d: %w", failIdx, failErr)
	}
	return ctx.Err()
}
