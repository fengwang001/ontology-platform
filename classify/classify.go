// Package classify 定义调用结果的错误分类与四类哨兵错误。
package classify

import (
	"context"
	"errors"
)

// Kind 是真实调用失败的类别。
type Kind int

const (
	// Retryable 可重试失败（含 panic 转成的错误）。
	Retryable Kind = iota
	// NonRetryable 不可重试失败（由 Mark 显式标记）。
	NonRetryable
	// Timeout 超时失败。
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

// 四类哨兵错误，调用方可用 errors.Is 区分。
var (
	ErrTimeout      = errors.New("classify: call timeout")
	ErrBulkheadFull = errors.New("classify: bulkhead full")
	ErrCircuitOpen  = errors.New("classify: circuit open")
	ErrClockSkew    = errors.New("classify: clock moved backwards")
)

type nonRetryable struct{ err error }

func (e nonRetryable) Error() string { return e.err.Error() }
func (e nonRetryable) Unwrap() error { return e.err }

// Mark 把 err 标记为不可重试；nil 输入返回 nil。
func Mark(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryable{err: err}
}

// IsNonRetryable 报告 err 是否被 Mark 标记过。
func IsNonRetryable(err error) bool {
	var nr nonRetryable
	return errors.As(err, &nr)
}

// Of 把真实调用返回的错误分类。nil 不属于任何失败类别，返回 Retryable。
func Of(err error) Kind {
	switch {
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return Timeout
	case IsNonRetryable(err):
		return NonRetryable
	default:
		return Retryable
	}
}
