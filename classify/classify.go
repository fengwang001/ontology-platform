// Package classify 把调用错误分类为可重试、不可重试与超时三类。
package classify

import (
	"context"
	"errors"
)

// Kind 是错误的类别。
type Kind int

const (
	// None 表示没有错误（调用成功）。
	None Kind = iota
	// Retryable 表示可重试的临时性错误（未显式标记的错误的默认类别）。
	Retryable
	// NonRetryable 表示不可重试的错误。
	NonRetryable
	// Timeout 表示超时错误。
	Timeout
)

// String 返回类别的可读名称。
func (k Kind) String() string {
	switch k {
	case None:
		return "none"
	case Retryable:
		return "retryable"
	case NonRetryable:
		return "non-retryable"
	case Timeout:
		return "timeout"
	}
	return "unknown"
}

// ErrTimeout 是超时错误的判定哨兵，可用 errors.Is 匹配。
var ErrTimeout = errors.New("classify: timeout")

// ErrPanic 是调用 panic 被捕获后转换的判定哨兵，可用 errors.Is 匹配。
var ErrPanic = errors.New("classify: panic")

type nonRetryable struct{ err error }

func (e nonRetryable) Error() string { return e.err.Error() }
func (e nonRetryable) Unwrap() error { return e.err }

// MarkNonRetryable 把 err 标记为不可重试；err 为 nil 时返回 nil。
func MarkNonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryable{err: err}
}

// Of 返回 err 的类别；nil 返回 None。
func Of(err error) Kind {
	if err == nil {
		return None
	}
	if errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return Timeout
	}
	if errors.Is(err, ErrPanic) {
		return NonRetryable
	}
	var nr nonRetryable
	if errors.As(err, &nr) {
		return NonRetryable
	}
	return Retryable
}
