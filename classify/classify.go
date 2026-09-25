// Package classify 把一次调用的错误归入三种可判定类别。
package classify

import (
	"context"
	"errors"
)

// Kind 是错误类别。
type Kind int

const (
	// Fatal 不可重试：重发不会改变结果。
	Fatal Kind = iota
	// Retryable 可重试：下游暂时故障。
	Retryable
	// Timeout 超时：按可恢复故障处理。
	Timeout
)

// 哨兵错误，业务可用 errors.Is 判定，也可包装后返回。
var (
	ErrFatal     = errors.New("classify: non-retryable error")
	ErrRetryable = errors.New("classify: retryable error")
	ErrTimeout   = errors.New("classify: timeout")
)

// Of 返回错误类别。nil 无意义（调用方只在失败时调用）。
// 未知错误保守归为 Fatal：不了解的故障不应驱动熔断。
func Of(err error) Kind {
	switch {
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return Timeout
	case errors.Is(err, ErrRetryable):
		return Retryable
	default:
		return Fatal
	}
}

// CountsAsFailure 报告该类别是否计入熔断失败。
func CountsAsFailure(k Kind) bool { return k == Retryable || k == Timeout }
