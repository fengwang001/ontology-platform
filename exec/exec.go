// Package exec 执行任务函数并收集结果：panic 被捕获并转为失败。
package exec

import (
	"context"

	"ontology/fail"
)

// Func 是任务函数；ctx 取消时应尽快返回，返回值即任务结果。
type Func func(ctx context.Context) error

// Result 是一次执行的收集结果。
type Result struct {
	Err error // 非 nil 表示失败；panic 被包装为 *fail.PanicError
}

// Run 同步执行 fn 并收集结果；panic 不会逃逸，转为 *fail.PanicError。
func Run(ctx context.Context, fn Func) (r Result) {
	defer func() {
		if v := recover(); v != nil {
			r = Result{Err: &fail.PanicError{Value: v}}
		}
	}()
	return Result{Err: fn(ctx)}
}
