// Package classify 把一次调用返回的错误归入四类之一。
package classify

import (
	"context"
	"errors"
)

// 调用结果类别，彼此可用 errors.Is 判定。
var (
	ErrRetryable    = errors.New("classify: retryable failure")
	ErrNonRetryable = errors.New("classify: non-retryable failure")
	ErrTimeout      = errors.New("classify: timeout")
)

// Class 是错误类别。
type Class int

const (
	ClassSuccess Class = iota
	ClassRetryable
	ClassNonRetryable
	ClassTimeout
)

// Retryable / NonRetryable / Timeout 用于显式标记错误类别。
func Retryable(err error) error    { return wrap(ErrRetryable, err) }
func NonRetryable(err error) error { return wrap(ErrNonRetryable, err) }
func Timeout(err error) error      { return wrap(ErrTimeout, err) }

func wrap(marker, err error) error {
	if err == nil {
		return marker
	}
	return errors.Join(marker, err)
}

// Of 返回 err 的类别。判定顺序：超时 > 不可重试 > 可重试；
// context.DeadlineExceeded 与实现 Timeout() bool 的错误视为超时，
// context.Canceled 视为不可重试；未标记的普通错误视为可重试。
func Of(err error) Class {
	switch {
	case err == nil:
		return ClassSuccess
	case errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded):
		return ClassTimeout
	case isTimeout(err):
		return ClassTimeout
	case errors.Is(err, ErrNonRetryable) || errors.Is(err, context.Canceled):
		return ClassNonRetryable
	default:
		return ClassRetryable
	}
}

type timeoutError interface{ Timeout() bool }

func isTimeout(err error) bool {
	var te timeoutError
	return errors.As(err, &te) && te.Timeout()
}

// CountsBreaker 报告该错误是否进入熔断窗口：
// 仅可重试失败与超时计入；不可重试失败不触发熔断。
func CountsBreaker(err error) bool {
	c := Of(err)
	return c == ClassRetryable || c == ClassTimeout
}
