// Package classify 把调用返回的错误分为可重试、不可重试、超时三类。
package classify

import (
	"context"
	"errors"

	"ontology/timeout"
)

// ErrNonRetryable 标记不可重试错误，可用 fmt.Errorf("%w") 包装。
var ErrNonRetryable = errors.New("non-retryable error")

// Kind 是错误类别。
type Kind int

const (
	// Retryable 默认类别：下游临时故障，重试可能成功。
	Retryable Kind = iota
	// NonRetryable 契约性错误，重试无意义，但仍计入熔断失败。
	NonRetryable
	// Timeout 单次调用超时。
	Timeout
)

func (k Kind) String() string {
	switch k {
	case NonRetryable:
		return "non-retryable"
	case Timeout:
		return "timeout"
	default:
		return "retryable"
	}
}

// Of 返回 err 的类别；err 为 nil 时返回 Retryable（调用方不应传 nil）。
func Of(err error) Kind {
	switch {
	case errors.Is(err, timeout.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return Timeout
	case errors.Is(err, ErrNonRetryable):
		return NonRetryable
	default:
		return Retryable
	}
}
