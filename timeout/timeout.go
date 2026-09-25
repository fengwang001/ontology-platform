// Package timeout 为单次调用供时限控制。
package timeout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ontology/classify"
)

// ErrInvalidDuration 表示时限非法（<= 0）。
var ErrInvalidDuration = errors.New("timeout: duration must be positive")

// Do 在 d 时限内执行 fn。
// 超时返回包装 classify.ErrTimeout 的错误；上游 ctx 取消返回 ctx.Err()；
// fn panic 被捕获并转换为包装 classify.ErrPanic 的错误，不会击穿调用方。
func Do(ctx context.Context, d time.Duration, fn func(context.Context) error) error {
	if d <= 0 {
		return ErrInvalidDuration
	}
	cctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	type result struct{ err error }
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				ch <- result{err: fmt.Errorf("%w: %v", classify.ErrPanic, v)}
			}
		}()
		ch <- result{err: fn(cctx)}
	}()
	select {
	case r := <-ch:
		return r.err
	case <-cctx.Done():
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: exceeded %s", classify.ErrTimeout, d)
	}
}
