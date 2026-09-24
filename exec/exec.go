// Package exec 执行单个任务函数并把 panic 转成失败错误。
package exec

import (
	"context"

	"ontology/fail"
)

// Func 是任务函数；ctx 在快速失败取消时被关闭。
type Func func(ctx context.Context) error

// Run 同步执行 fn；fn panic 时 recover 并返回 *fail.PanicError，
// 调度器本身绝不因任务 panic 而崩溃。
func Run(ctx context.Context, fn Func) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &fail.PanicError{Value: r}
		}
	}()
	return fn(ctx)
}
