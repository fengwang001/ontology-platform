// Package timeout 提供单次调用的时限控制。
package timeout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ontology/classify"
)

// Do 在时限 d 内执行 fn。超时返回包裹 classify.ErrTimeout 的错误；
// fn 的 panic 在 fn 所在 goroutine 内捕获，转为包裹 classify.ErrPanic 的错误，
// 不会击穿包装器。fn 返回后其结果原样透传。
func Do(ctx context.Context, d time.Duration, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	done := make(chan error, 1) // 缓冲保证超时后 fn 完成不会泄漏 goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("%w: %v", classify.ErrPanic, r)
			}
		}()
		done <- fn(ctx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w after %s", classify.ErrTimeout, d)
		}
		return ctx.Err()
	}
}
