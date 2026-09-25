// Package classify 把一次调用返回的错误归入确定的类别。
package classify

import (
	"context"
	"errors"
	"fmt"
)

// Category 是错误类别。
type Category int

const (
	Retryable    Category = iota // 可重试的瞬时失败
	NonRetryable                 // 确定性失败，重试无意义
	Timeout                      // 超时或上游取消
	PanicKind                    // 调用以 panic 结束
)

// 哨兵错误，可通过 errors.Is 判定。
var (
	ErrRetryable    = errors.New("classify: retryable error")
	ErrNonRetryable = errors.New("classify: non-retryable error")
	ErrTimeout      = errors.New("classify: timeout error")
	ErrPanic        = errors.New("classify: call panicked")
)

type classified struct {
	err error
	cat Category
}

func (c *classified) Error() string { return c.err.Error() }
func (c *classified) Unwrap() error { return c.err }

// Wrap 以指定类别包装 err；返回值可用 Classify 还原类别，
// 且 errors.Is(result, 对应哨兵) 成立。
func Wrap(cat Category, err error) error {
	sentinel := ErrRetryable
	switch cat {
	case NonRetryable:
		sentinel = ErrNonRetryable
	case Timeout:
		sentinel = ErrTimeout
	case PanicKind:
		sentinel = ErrPanic
	}
	return &classified{err: errors.Join(sentinel, err), cat: cat}
}

// PanicError 把一次 panic 恢复值规范化为 PanicKind 类别错误。
func PanicError(v any) error {
	return Wrap(PanicKind, panicValue{v})
}

type panicValue struct{ v any }

func (e panicValue) Error() string {
	if err, ok := e.v.(error); ok {
		return "panic: " + err.Error()
	}
	return "panic: " + fmt.Sprint(e.v)
}

func (e panicValue) Unwrap() error {
	if err, ok := e.v.(error); ok {
		return err
	}
	return nil
}

// Classify 判定错误类别。context 超时/取消归 Timeout；
// 已包装错误按包装类别；其余默认视为可重试。
func Classify(err error) Category {
	if err == nil {
		return Retryable
	}
	var c *classified
	if errors.As(err, &c) {
		return c.cat
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return Timeout
	case errors.Is(err, ErrTimeout):
		return Timeout
	case errors.Is(err, ErrNonRetryable):
		return NonRetryable
	case errors.Is(err, ErrPanic):
		return PanicKind
	default:
		return Retryable
	}
}
