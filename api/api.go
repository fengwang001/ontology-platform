// Package api 对外门面：New/Start/Step/Commit/Crash/View/Gen/SelfCheck 与哨兵错误。依赖 reb。
package api

import "ontology/reb"

// 哨兵错误（再导出，四种故障互不相同，可用 errors.Is 判定）。
var (
	ErrBadChunk    = reb.ErrBadChunk    // New 的 chunk < 1
	ErrBusy        = reb.ErrBusy        // Start 时已在重建
	ErrNotBuilding = reb.ErrNotBuilding // Step/Commit/Crash 时未在重建
	ErrIncomplete  = reb.ErrIncomplete  // Commit 时未处理完
)

// Rebuilder 是带检查点的物化视图重建器；方法集由 reb 实现。
type Rebuilder = reb.Rebuilder

// New 构造 Rebuilder；chunk < 1 返回 ErrBadChunk。
func New(src []string, chunk int) (*Rebuilder, error) {
	return reb.New(src, chunk)
}

// SelfCheck 对内置操作序列核验四条不变量；全部通过返回 nil。
func SelfCheck() error {
	return reb.SelfCheck()
}
