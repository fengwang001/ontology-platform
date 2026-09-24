// Package pmap 有界重排窗口的保序并行映射：N 个 worker 并行处理输入，
// 结果按输入下标严格递增 emit，暂存的乱序结果不超过窗口 W。
package pmap

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/window"
)

// ErrBadConfig 表示 n < 1 或 w < 1。
var ErrBadConfig = errors.New("pmap: bad config")

var lastMaxHeld atomic.Int64

// LastMaxHeld 返回最近一次 Run 中窗口的历史最大暂存数（供校验用）。
func LastMaxHeld() int { return int(lastMaxHeld.Load()) }

type result[Out any] struct {
	idx int
	out Out
	err error
}

// Run 并行处理 inputs 并按序 emit。任一输入失败时，取消其余在跑的 fn，
// 等它们全部退出后返回「失败下标最小」的包装错误（见 NOTES.md 第三节）。
func Run[In, Out any](ctx context.Context, inputs []In, n, w int, fn func(context.Context, In) (Out, error), emit func(i int, out Out)) error {
	if n < 1 || w < 1 {
		return ErrBadConfig
	}
	parent := ctx
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var wg sync.WaitGroup
	defer wg.Wait() // 返回前保证全部 worker goroutine 已退出
	win := window.New(w)
	defer func() { lastMaxHeld.Store(int64(win.MaxHeld())) }()
	res := make(chan result[Out])
	inflight, admit, nextEmit, minErr := 0, 0, 0, -1
	var errAt error
	for inflight > 0 || (admit < len(inputs) && minErr < 0) {
		for admit < len(inputs) && inflight < n && minErr < 0 && parent.Err() == nil && win.Admit(admit) {
			i, in := admit, inputs[admit]
			inflight++
			admit++
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, err := fn(ctx, in)
				res <- result[Out]{i, out, err}
			}()
		}
		select {
		case r := <-res:
			inflight--
			if r.err != nil {
				if ctx.Err() != nil && errors.Is(r.err, ctx.Err()) {
					continue // 内部取消造成的伪失败：无结果，也不计入失败下标
				}
				if minErr < 0 || r.idx < minErr {
					minErr, errAt = r.idx, r.err
				}
				cancel()
				continue
			}
			win.Put(r.idx, r.out)
			for win.Ready() && (minErr < 0 || nextEmit < minErr) {
				emit(nextEmit, win.Pop().(Out))
				nextEmit++
			}
		case <-parent.Done():
			cancel()
			for inflight > 0 {
				<-res
				inflight--
			}
			return parent.Err()
		}
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if minErr >= 0 {
		return fmt.Errorf("pmap: input %d: %w", minErr, errAt)
	}
	return nil
}
