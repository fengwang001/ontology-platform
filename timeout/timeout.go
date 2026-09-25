// Package timeout 为单次调用提供时限控制，并把 panic 捕获转换为错误。
package timeout

import (
	"time"

	"ontology/classify"
)

type outcome struct{ err error }

// Do 在独立 goroutine 中执行 fn：
//   - fn 正常返回：原样透传其 error；
//   - 超过 limit：返回 classify.ErrTimeout（fn 可能仍在后台运行，其结果被丢弃）；
//   - fn panic：捕获并转换为可用 errors.Is(classify.ErrPanic) 判定的错误；
//   - limit <= 0：不加时限，直接同步执行（仍会捕获 panic）。
func Do(limit time.Duration, fn func() error) error {
	if limit <= 0 {
		return safeCall(fn)
	}
	done := make(chan outcome, 1)
	go func() {
		done <- outcome{err: safeCall(fn)}
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case o := <-done:
		return o.err
	case <-timer.C:
		return classify.ErrTimeout
	}
}

func safeCall(fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = classify.PanicError(v)
		}
	}()
	return fn()
}
