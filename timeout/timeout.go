// Package timeout 给单次调用加时限并把 panic 转成普通错误。
package timeout

import (
	"context"
	"errors"
	"time"

	"ontology/classify"
)

// ErrInvalidTimeout 表示时限配置非法（非正）。
var ErrInvalidTimeout = errors.New("timeout: duration must be positive")

type result struct{ err error }

// Run 在 d 时限内执行 fn。fn 正常结束返回其错误；超时时返回包装过的
// context.DeadlineExceeded（classify 归为 Timeout）；fn panic 时恢复并
// 返回 classify.PanicError，不会击穿调用方。
func Run(parent context.Context, d time.Duration,
	fn func(ctx context.Context) error) error {
	if d <= 0 {
		return ErrInvalidTimeout
	}
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()

	resCh := make(chan result, 1)
	go func() {
		var res result
		defer func() {
			if r := recover(); r != nil {
				res.err = classify.PanicError(r)
			}
			resCh <- res
		}()
		res.err = fn(ctx)
	}()

	select {
	case res := <-resCh:
		return res.err
	case <-ctx.Done():
		<-resCh // 等 fn 退出，避免 goroutine 泄漏与数据竞争
		return classify.Wrap(classify.Timeout, ctx.Err())
	}
}
