// Package exec 执行单个任务函数，并把 panic 捕获为失败结果。
package exec

import (
	"context"
	"errors"
	"fmt"
)

// ErrPanic 标记任务执行中发生了 panic，可用 errors.Is 判定。
var ErrPanic = errors.New("task panicked")

// PanicError 包装被捕获的 panic 原始值。
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

func (e *PanicError) Unwrap() error { return ErrPanic }

// Func 是任务执行函数，返回非 nil 错误即视为失败。
type Func func(ctx context.Context) error

// Result 是一次任务执行的结果。
type Result struct {
	Err      error
	Panicked bool
}

// Run 执行 f；f 发生 panic 时捕获并转换为失败结果，绝不向上抛出。
func Run(ctx context.Context, f Func) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			res = Result{Err: &PanicError{Value: r}, Panicked: true}
		}
	}()
	return Result{Err: f(ctx)}
}
