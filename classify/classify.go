// Package classify 把调用错误分为可重试、不可重试、超时三类。
package classify

import (
	"context"
	"errors"
)

// Kind 是错误类别。
type Kind int

const (
	// Retryable 表示下游临时故障，重试可能成功，计入熔断。
	Retryable Kind = iota
	// NonRetryable 表示调用方错误，重试无意义，不计入熔断。
	NonRetryable
	// Timeout 表示调用超时，反映下游处理能力，计入熔断。
	Timeout
)

// ErrNonRetryable 用于把错误标记为不可重试。
var ErrNonRetryable = errors.New("classify: non-retryable")

// errTimeoutSentinel 由 timeout 包的错误包装实现匹配，见 IsTimeout。
var errTimeoutSentinel = errors.New("classify: timeout")

// Mark 把 err 标记为不可重试；err 为 nil 时返回 nil。
func Mark(err error) error {
	if err == nil {
		return nil
	}
	return &kindError{kind: NonRetryable, err: err}
}

// MarkTimeout 把 err 标记为超时；err 为 nil 时返回 nil。
func MarkTimeout(err error) error {
	if err == nil {
		return nil
	}
	return &kindError{kind: Timeout, err: err}
}

type kindError struct {
	kind Kind
	err  error
}

func (e *kindError) Error() string { return e.err.Error() }
func (e *kindError) Unwrap() error { return e.err }

// Is 让 errors.Is(err, ErrNonRetryable) 与超时判定可用。
func (e *kindError) Is(target error) bool {
	switch e.kind {
	case NonRetryable:
		return target == ErrNonRetryable
	case Timeout:
		return target == errTimeoutSentinel
	}
	return false
}

// IsTimeout 报告 err 是否被标记为超时。
func IsTimeout(err error) bool {
	return errors.Is(err, errTimeoutSentinel) || errors.Is(err, context.DeadlineExceeded)
}

// Of 返回 err 的类别：显式标记优先，其次 context 超时，默认可重试。
func Of(err error) Kind {
	if err == nil {
		panic("classify.Of: nil error")
	}
	if errors.Is(err, ErrNonRetryable) {
		return NonRetryable
	}
	if IsTimeout(err) {
		return Timeout
	}
	return Retryable
}

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
