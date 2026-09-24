// Package classify 定义调用失败的错误类别与分类函数。
package classify

import (
	"context"
	"errors"
)

// Kind 是失败的错误类别。
type Kind int

const (
	// KindNone 表示无错误。
	KindNone Kind = iota
	// KindRetryable 可重试失败：计入熔断。
	KindRetryable
	// KindNonRetryable 不可重试（确定性）失败：真实失败但不计入熔断。
	KindNonRetryable
	// KindTimeout 超时：计入熔断。
	KindTimeout
	// KindPanic 调用 panic 被捕获：计入熔断。
	KindPanic
)

// 四类哨兵错误，调用方可用 errors.Is 精确区分。
var (
	// ErrRetryable 表示一次可重试的真实失败。
	ErrRetryable = errors.New("classify: retryable failure")
	// ErrNonRetryable 表示一次不可重试的确定性失败。
	ErrNonRetryable = errors.New("classify: non-retryable failure")
	// ErrTimeout 表示调用超时。
	ErrTimeout = errors.New("classify: call timeout")
	// ErrPanic 表示被包装的调用发生 panic。
	ErrPanic = errors.New("classify: call panicked")
)

// Of 把任意错误映射为类别；nil 返回 KindNone。
// 先按哨兵错误判定，context 超时归入 KindTimeout，其余视为可重试。
func Of(err error) Kind {
	switch {
	case err == nil:
		return KindNone
	case errors.Is(err, ErrTimeout):
		return KindTimeout
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	case errors.Is(err, ErrNonRetryable):
		return KindNonRetryable
	case errors.Is(err, ErrPanic):
		return KindPanic
	case errors.Is(err, ErrRetryable):
		return KindRetryable
	default:
		return KindRetryable
	}
}

// BreakerFailure 报告该类别是否计入熔断失败计数。
// 可重试失败、超时、panic 计；不可重试失败中立。
func (k Kind) BreakerFailure() bool {
	switch k {
	case KindRetryable, KindTimeout, KindPanic:
		return true
	default:
		return false
	}
}

// Sentinel 返回该类别对应的哨兵错误；KindNone 返回 nil。
func (k Kind) Sentinel() error {
	switch k {
	case KindRetryable:
		return ErrRetryable
	case KindNonRetryable:
		return ErrNonRetryable
	case KindTimeout:
		return ErrTimeout
	case KindPanic:
		return ErrPanic
	default:
		return nil
	}
}
