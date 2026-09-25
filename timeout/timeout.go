// Package timeout 为单次调用提供时限控制，并把 fn 的 panic 捕获为错误。
package timeout

import (
	"context"
	"fmt"
	"time"
)

// ErrTimeout 在调用超时时返回，包装 context.DeadlineExceeded，
// 因此 errors.Is(err, context.DeadlineExceeded) 同样成立。
var ErrTimeout = fmt.Errorf("timeout: %w", context.DeadlineExceeded)

// PanicError 把 fn 的 panic 转为普通错误，避免击穿包装器。
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("timeout: panic recovered: %v", e.Value)
}

// Do 在 d 时限内运行 fn。超时返回 ErrTimeout（fn 的 goroutine 可能仍在
// 运行，但从调用方视角调用已结束）；上游取消返回 ctx 的错误；fn panic
// 时 recover 并返回 *PanicError；否则返回 fn 自身的返回值。
func Do(ctx context.Context, d time.Duration, fn func(context.Context) error) error {
	if d <= 0 {
		return fmt.Errorf("timeout: non-positive duration %v", d)
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- &PanicError{Value: r}
			}
		}()
		done <- fn(ctx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return ErrTimeout
		}
		return ctx.Err()
	}
}
