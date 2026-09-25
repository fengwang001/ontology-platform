// Package timeout 为单次调用提供时限控制：超时返回可判定错误，
// 调用内 panic 被捕获并转为普通错误，不会击穿包装器。
package timeout

import (
	"context"
	"fmt"
	"time"

	"ontology/classify"
)

// Do 在 d 时限内执行 fn。超时返回包装了 classify.ErrTimeout 的错误；
// fn 内 panic 被 recover 并转为错误返回。时限通过 context 表达，
// 调用方可用自定义 ctx 注入任意截止时间，故时钟是可注入的。
func Do(ctx context.Context, d time.Duration, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("timeout: panic recovered: %v", r)
			}
			done <- result{err}
		}()
		err = fn(ctx)
	}()

	select {
	case r := <-done:
		return r.err
	case <-ctx.Done():
		return fmt.Errorf("%w: call exceeded %s", classify.ErrTimeout, d)
	}
}
