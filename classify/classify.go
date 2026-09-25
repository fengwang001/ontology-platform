// Package classify 对调用错误分类：可重试 / 不可重试 / 超时 / panic，
// 并给出「是否计入熔断失败」的唯一判定入口。
package classify

import "errors"

var (
	// ErrRetryable 表示下游临时故障，重试可能成功。
	ErrRetryable = errors.New("retryable error")
	// ErrNonRetryable 表示调用方契约错误（如参数非法），重试无意义。
	ErrNonRetryable = errors.New("non-retryable error")
	// ErrTimeout 表示单次调用超出时限。
	ErrTimeout = errors.New("call timeout")
	// ErrPanic 表示被调用函数发生 panic，已被捕获转换。
	ErrPanic = errors.New("call panicked")
)

// Kind 是错误类别。
type Kind int

const (
	KindNone Kind = iota
	KindRetryable
	KindNonRetryable
	KindTimeout
	KindPanic
)

func (k Kind) String() string {
	switch k {
	case KindRetryable:
		return "retryable"
	case KindNonRetryable:
		return "non-retryable"
	case KindTimeout:
		return "timeout"
	case KindPanic:
		return "panic"
	default:
		return "none"
	}
}

// PanicError 把 panic 值包装为可用 errors.Is(ErrPanic) 判定的错误。
func PanicError(v any) error {
	return &panicError{v: v}
}

type panicError struct{ v any }

func (e *panicError) Error() string { return ErrPanic.Error() }
func (e *panicError) Is(target error) bool {
	return target == ErrPanic
}

// Classify 返回 err 的类别；err 为 nil 时返回 KindNone。
// 未包装任何已知哨兵的错误按可重试处理（保守计入熔断）。
func Classify(err error) Kind {
	switch {
	case err == nil:
		return KindNone
	case errors.Is(err, ErrTimeout):
		return KindTimeout
	case errors.Is(err, ErrPanic):
		return KindPanic
	case errors.Is(err, ErrNonRetryable):
		return KindNonRetryable
	default:
		return KindRetryable
	}
}

// IsBreakerFailure 判定该错误是否计入熔断失败统计。
// 不可重试错误是调用方契约问题，与下游健康无关，不计入（见 DESIGN.md 第 4 节）。
func IsBreakerFailure(err error) bool {
	switch Classify(err) {
	case KindRetryable, KindTimeout, KindPanic:
		return true
	default:
		return false
	}
}
