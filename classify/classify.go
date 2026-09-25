// Package classify 把调用错误分为可重试、不可重试、超时三类，
// 供熔断器决定是否计入失败样本、供统计包按类别细分。
package classify

import (
	"errors"
	"fmt"
)

// Kind 是错误类别。
type Kind int

const (
	// Retryable 指向下游处理能力受损，计入熔断失败样本。
	Retryable Kind = iota
	// NonRetryable 是调用方契约问题，不计入熔断样本。
	NonRetryable
	// Timeout 单次调用超时，计入熔断失败样本。
	Timeout
	// NumKinds 是类别总数，用于按类别细分的数组长度。
	NumKinds
)

func (k Kind) String() string {
	switch k {
	case Retryable:
		return "retryable"
	case NonRetryable:
		return "non-retryable"
	case Timeout:
		return "timeout"
	}
	return "unknown"
}

// 哨兵错误，均可用 errors.Is 判定。
var (
	// ErrTimeout 表示单次调用超时。
	ErrTimeout = errors.New("classify: call timeout")
	// ErrNonRetryable 表示不可重试错误，用 MarkNonRetryable 包装。
	ErrNonRetryable = errors.New("classify: non-retryable error")
	// ErrPanic 表示调用发生 panic，已被捕获转为错误。
	ErrPanic = errors.New("classify: panic in call")
)

// MarkNonRetryable 把 err 标记为不可重试；err 为 nil 时返回裸哨兵。
func MarkNonRetryable(err error) error {
	if err == nil {
		return ErrNonRetryable
	}
	return fmt.Errorf("%w: %w", ErrNonRetryable, err)
}

// Of 返回 err 的类别；nil 视为调用方传错，按可重试处理。
func Of(err error) Kind {
	switch {
	case errors.Is(err, ErrNonRetryable):
		return NonRetryable
	case errors.Is(err, ErrTimeout):
		return Timeout
	default:
		return Retryable
	}
}
