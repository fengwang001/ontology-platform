// Package classify 把一次真实调用的错误分为三类：可重试、不可重试、超时。
// 分类结果同时服务于熔断计数（不可重试不计入）与统计细分。
package classify

import (
	"context"
	"errors"
)

// Kind 是错误类别。
type Kind int

const (
	// KindRetryable 可重试：下游可能故障，计入熔断失败。未知错误的默认值。
	KindRetryable Kind = iota
	// KindNonRetryable 不可重试：下游正常处理并拒绝，不计入熔断失败。
	KindNonRetryable
	// KindTimeout 超时：单次调用超时时限，计入熔断失败。
	KindTimeout
)

// NumKinds 是类别总数，供按类别细分的数组定界。
const NumKinds = 3

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

// Kinder 由能自我声明类别的错误实现。
type Kinder interface {
	CallKind() Kind
}

// Of 返回 err 的类别。err 为 nil 时视为调用成功，返回 KindRetryable
// （该返回值在成功路径上不会被使用）。判定顺序：Kinder 接口优先，
// 其次 context.DeadlineExceeded，其余默认可重试。
func Of(err error) Kind {
	var k Kinder
	if errors.As(err, &k) {
		return k.CallKind()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return KindTimeout
	}
	return KindRetryable
}

// CountsTowardBreaker 报告该类别是否计入熔断失败。
func CountsTowardBreaker(k Kind) bool {
	return k != KindNonRetryable
}

type kindError struct {
	kind Kind
	err  error
}

func (e *kindError) Error() string { return e.err.Error() }
func (e *kindError) Unwrap() error { return e.err }
func (e *kindError) CallKind() Kind {
	return e.kind
}

// Mark 把 err 标记为指定类别；err 为 nil 时返回 nil。
func Mark(err error, kind Kind) error {
	if err == nil {
		return nil
	}
	return &kindError{kind: kind, err: err}
}

// NonRetryable 把 err 标记为不可重试错误。
func NonRetryable(err error) error {
	return Mark(err, KindNonRetryable)
}
