// Package classify 把一次调用的错误分为可重试、不可重试、超时三类，
// 供熔断器决定哪些失败计入健康度统计。
package classify

import (
	"context"
	"errors"
)

// Kind 是错误的类别。
type Kind int

const (
	// Retryable 表示下游临时故障，重试可能成功，计入熔断统计。
	Retryable Kind = iota
	// NonRetryable 表示调用方错误（如参数非法），不计入熔断统计。
	NonRetryable
	// Timeout 表示调用超时，计入熔断统计。
	Timeout
)

// NumKind 是类别总数，用于按类别细分的数组下标边界。
const NumKind = 3

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

// ErrNonRetryable 是不可重试错误的标记，调用方用
// fmt.Errorf("...: %w", classify.ErrNonRetryable) 包装业务错误。
var ErrNonRetryable = errors.New("classify: non-retryable error")

// KindOf 判定 err 的类别：超时（含 context.DeadlineExceeded）优先，
// 其次是显式标记的不可重试，其余默认按可重试处理。
func KindOf(err error) Kind {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return Timeout
	case errors.Is(err, ErrNonRetryable):
		return NonRetryable
	default:
		return Retryable
	}
}
