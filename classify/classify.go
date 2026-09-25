// Package classify 把调用结果错误分为可重试、不可重试、超时三类，
// 并给出该类别是否应触发熔断计数的判定。
package classify

import (
	"context"
	"errors"
)

// Kind 是错误类别。
type Kind int

const (
	// KindRetryable 可重试错误：下游临时故障，计入熔断。
	KindRetryable Kind = iota
	// KindNonRetryable 不可重试错误：调用方问题，不计入熔断。
	KindNonRetryable
	// KindTimeout 超时错误：下游过慢，计入熔断。
	KindTimeout
)

// ErrNonRetryable 用于把错误标记为不可重试：fmt.Errorf("%w: ...", ErrNonRetryable)。
var ErrNonRetryable = errors.New("classify: non-retryable")

// ErrTimeout 用于把错误标记为超时：fmt.Errorf("%w: ...", ErrTimeout)。
var ErrTimeout = errors.New("classify: timeout")

// String 返回类别名，用于日志与演示输出。
func (k Kind) String() string {
	switch k {
	case KindNonRetryable:
		return "non-retryable"
	case KindTimeout:
		return "timeout"
	default:
		return "retryable"
	}
}

// Trips 报告该类别是否计入熔断失败统计。
// 不可重试错误是调用方问题，不反映下游健康，故不触发熔断。
func (k Kind) Trips() bool { return k != KindNonRetryable }

// Of 对非 nil 错误分类：超时优先，其次不可重试，默认可重试。
func Of(err error) Kind {
	switch {
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	case errors.Is(err, ErrNonRetryable):
		return KindNonRetryable
	default:
		return KindRetryable
	}
}
