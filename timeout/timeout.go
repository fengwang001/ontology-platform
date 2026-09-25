// Package timeout 给单次调用加时限，并把 panic 转成普通失败。
package timeout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ontology/classify"
)

// ErrTimedOut 在调用超过时限时返回，errors.Is 可判定为 classify.ErrTimeout。
var ErrTimedOut = fmt.Errorf("timeout: %w", classify.ErrTimeout)

// Do 在 d 时限内执行 fn。fn 使用派生 ctx 以便感知截止。
// - 截止先到：返回 ErrTimedOut（fn goroutine 不被强杀，结果经缓冲通道回收）。
// - 上游 ctx 先取消：返回 ctx.Err()。
// - fn panic：捕获并包装为可重试错误返回，绝不击穿调用方。
func Do(ctx context.Context, d time.Duration, fn func(context.Context) error) error {
	if d <= 0 {
		return errors.New("timeout: duration must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	type result struct {
		err   error
		panic any
	}
	done := make(chan result, 1)
	go func() {
		res := result{}
		defer func() {
			if r := recover(); r != nil {
				res.panic = r
			}
			done <- res
		}()
		res.err = fn(ctx)
	}()

	select {
	case r := <-done:
		if r.panic != nil {
			return fmt.Errorf("timeout: call panicked: %v: %w", r.panic, classify.ErrRetryable)
		}
		return r.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			<-done // 回收迟到的结果，避免泄漏 goroutine 写通道
			return ErrTimedOut
		}
		<-done
		return ctx.Err()
	}
}
