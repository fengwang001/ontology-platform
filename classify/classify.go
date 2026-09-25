// Package classify 提供错误分类、可注入时钟与四类可判定哨兵错误。
package classify

import (
	"errors"
	"time"
)

// Class 是一次真实调用结果的错误类别。
type Class int

const (
	OK         Class = iota // 成功
	Retryable               // 可重试失败（含 panic 转化、未知错误）
	Permanent               // 不可重试失败，不计入熔断
	TimeoutC                // 超时
)

func (c Class) String() string {
	switch c {
	case OK:
		return "ok"
	case Retryable:
		return "retryable"
	case Permanent:
		return "permanent"
	case TimeoutC:
		return "timeout"
	default:
		return "unknown"
	}
}

var (
	// ErrBreakerOpen 被熔断器拒绝（打开或半开探测名额用尽）。
	ErrBreakerOpen = errors.New("breaker open")
	// ErrBulkheadRejected 舱壁在途与队列均满，立即拒绝。
	ErrBulkheadRejected = errors.New("bulkhead rejected")
	// ErrTimeout 单次调用超过时限。
	ErrTimeout = errors.New("call timeout")
	// ErrClockBackwards 注入时钟发生回拨，冷却判定拒绝且状态不变。
	ErrClockBackwards = errors.New("clock moved backwards")
	// ErrPermanent 不可重试错误的标记，可用 errors.Is 判别。
	ErrPermanent = errors.New("permanent error")
)

// MarkPermanent 把 err 标记为不可重试错误。
func MarkPermanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

type permanentError struct{ err error }

func (e permanentError) Error() string { return ErrPermanent.Error() + ": " + e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }
func (e permanentError) Is(target error) bool { return target == ErrPermanent }

// Classify 按错误类别归类。nil 为 OK。
func Classify(err error) Class {
	switch {
	case err == nil:
		return OK
	case errors.Is(err, ErrTimeout):
		return TimeoutC
	case errors.Is(err, ErrPermanent):
		return Permanent
	default:
		return Retryable
	}
}

// Clock 是熔断器冷却判定使用的可注入时钟。
type Clock interface {
	Now() time.Time
}

// RealClock 使用系统墙钟。
type RealClock struct{}

// Now 返回当前本地时间。
func (RealClock) Now() time.Time { return time.Now() }

// FakeClock 是手动推进的测试时钟。
type FakeClock struct{ T time.Time }

// NewFakeClock 以 t 为起点构造假时钟。
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{T: t} }

// Now 返回当前假时间。
func (c *FakeClock) Now() time.Time { return c.T }

// Advance 向前推进假时间。
func (c *FakeClock) Advance(d time.Duration) { c.T = c.T.Add(d) }

// Rollback 人为回拨假时间，用于测试回拨防护。
func (c *FakeClock) Rollback(d time.Duration) { c.T = c.T.Add(-d) }
