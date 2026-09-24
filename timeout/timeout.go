// Package timeout 为单次调用提供可注入时钟的时限控制。
package timeout

import (
	"context"
	"errors"
	"time"
)

// Clock 是可注入的时钟。
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer 与 time.Timer 同构。
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// RealClock 使用进程真实时钟。
type RealClock struct{}

// Now 返回当前时间。
func (RealClock) Now() time.Time { return time.Now() }

// NewTimer 返回真实定时器。
func (RealClock) NewTimer(d time.Duration) Timer { return &realTimer{t: time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time { return r.t.C }
func (r *realTimer) Stop() bool          { return r.t.Stop() }

// ErrTimedOut 表示调用超过时限。
var ErrTimedOut = errors.New("timeout: call exceeded deadline")

// ErrPanic 表示被调用函数发生 panic，已被捕获转为失败。
var ErrPanic = errors.New("timeout: call panicked")

// Run 在 d 时限内执行 fn：超时返回 ErrTimedOut（取消 ctx），
// panic 被捕获并包装为 ErrPanic，正常则原样返回 fn 的结果。
func Run(ctx context.Context, clock Clock, d time.Duration, fn func(ctx context.Context) error) error {
	if clock == nil {
		clock = RealClock{}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		var res result
		defer func() {
			if r := recover(); r != nil {
				res.err = errors.Join(ErrPanic, errors.New(toString(r)))
			}
			done <- res
		}()
		res.err = fn(ctx)
	}()
	timer := clock.NewTimer(d)
	defer timer.Stop()
	select {
	case res := <-done:
		return res.err
	case <-timer.C():
		cancel()
		<-done // 等待被调函数观察取消并退出，避免 goroutine 泄漏
		return ErrTimedOut
	case <-ctx.Done():
		<-done
		return ctx.Err()
	}
}

func toString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case error:
		return s.Error()
	default:
		return "panic"
	}
}
