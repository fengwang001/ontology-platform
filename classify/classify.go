// Package classify 把一次调用的错误归类为可重试、不可重试或超时。
package classify

import (
	"context"
	"errors"
)

// Kind 是错误的类别。
type Kind int

const (
	// Retryable 表示瞬时故障，重试可能成功；未知错误默认归入此类。
	Retryable Kind = iota
	// NonRetryable 表示永久性失败（如参数错误），重试无意义。
	NonRetryable
	// Timeout 表示调用超出时限。
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

// 类别标记哨兵错误，可用 errors.Is 判定。
var (
	ErrRetryable    = errors.New("classify: retryable error")
	ErrNonRetryable = errors.New("classify: non-retryable error")
	ErrTimeout      = errors.New("classify: timeout error")
)

// MarkRetryable 把 err 标记为可重试；err 为 nil 时返回 nil。
func MarkRetryable(err error) error {
	if err == nil {
		return nil
	}
	return &marked{kind: Retryable, err: err}
}

// MarkNonRetryable 把 err 标记为不可重试；err 为 nil 时返回 nil。
func MarkNonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return &marked{kind: NonRetryable, err: err}
}

type marked struct {
	kind Kind
	err  error
}

func (m *marked) Error() string { return m.err.Error() }
func (m *marked) Unwrap() error { return m.err }

func (m *marked) Is(target error) bool {
	switch m.kind {
	case NonRetryable:
		return target == ErrNonRetryable
	default:
		return target == ErrRetryable
	}
}

// Of 返回 err 的类别。超时优先（context.DeadlineExceeded 或 ErrTimeout），
// 其次不可重试标记，其余（含未标记的未知错误）一律按可重试处理。
func Of(err error) Kind {
	switch {
	case err == nil:
		return Retryable
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrTimeout):
		return Timeout
	case errors.Is(err, ErrNonRetryable):
		return NonRetryable
	default:
		return Retryable
	}
}
