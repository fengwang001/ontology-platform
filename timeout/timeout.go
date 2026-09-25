// Package timeout 提供单次调用的时限控制，并把 panic 捕获为错误。
package timeout

import (
	"context"
	"fmt"
	"time"

	"ontology/classify"
)

// Do 在 d 时限内运行 fn。超时返回包装了 classify.ErrTimeout 的错误；
// fn panic 时被 recover 并转为普通 error，不会击穿包装器。
// 注意：超时后 fn 仍在后台运行，调用方需自行保证其可安全遗弃。
func Do(ctx context.Context, d time.Duration, fn func(context.Context) error) error {
	if d <= 0 {
		return fmt.Errorf("timeout: non-positive limit %v", d)
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, fn)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timeout after %v: %w", d, classify.ErrTimeout)
	}
}

func run(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic recovered: %v", v)
		}
	}()
	return fn(ctx)
}
