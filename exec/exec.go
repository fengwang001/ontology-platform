// Package exec 负责单个任务的执行、panic 捕获与结果收集。
package exec

import (
	"context"

	"ontology/fail"
)

// Func 是任务执行函数，应尊重 ctx 取消，但不被强制要求。
type Func func(ctx context.Context) error

// Result 是一次执行的产出。
type Result struct {
	ID  string
	Err error
}

// Run 同步执行 fn 并 recover panic，把 panic 转成 *fail.PanicError，
// 保证任务崩溃不会拖垮调度器。
func Run(ctx context.Context, id string, fn Func) (res Result) {
	res.ID = id
	defer func() {
		if r := recover(); r != nil {
			res.Err = &fail.PanicError{Task: id, Value: r}
		}
	}()
	res.Err = fn(ctx)
	return res
}
