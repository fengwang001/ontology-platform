// Package timeout 为单次调用提供时限控制，并把 panic 捕获为普通错误。
package timeout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ontology/classify"
)

// ErrTimeout 表示单次调用超过时限。调用本身可能被遗弃在后台 goroutine。
var ErrTimeout = errors.New("timeout: call exceeded its deadline")

// PanicError 包装被捕获的 panic，panic 不会击穿包装器。
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("timeout: panic recovered: %v", e.Value)
}

// CallKind 实现 classify.Kinder：panic 后下游状态未知，保守按可重试计。
func (e *PanicError) CallKind() classify.Kind { return classify.KindRetryable }

type result struct {
	err error
}

// Do 在 d 时限内执行 fn。fn 收到带时限的 ctx；超时返回包装了 ErrTimeout
// 的错误；fn panic 时捕获并返回 *PanicError；上游 ctx 取消时返回 ctx.Err()。
func Do(ctx context.Context, d time.Duration, fn func(context.Context) error) error {
	if d <= 0 {
		return fmt.Errorf("timeout: duration must be > 0, got %s", d)
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			if p := recover(); p != nil {
				r.err = &PanicError{Value: p}
			}
			done <- r
		}()
		r.err = fn(ctx)
	}()
	select {
	case r := <-done:
		return r.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w after %s", ErrTimeout, d)
		}
		return ctx.Err()
	}
}
