// Package api 是撤回流增量 COUNT DISTINCT 的对外入口：
// New / Feed / View / SelfCheck。仅依赖 dagg，状态在进程内存。
package api

import "ontology/dagg"

// 类型别名：调用方直接使用 api.Change / api.Out。
type (
	Change = dagg.Change
	Out    = dagg.Out
)

// 可判定的哨兵错误（透出 dagg 的三类错误）。
var (
	ErrWithdraw = dagg.ErrWithdraw
	ErrInvalid  = dagg.ErrInvalid
	ErrLimit    = dagg.ErrLimit
)

// Engine 是对外聚合引擎。
type Engine struct {
	a *dagg.Agg
}

// New 创建全局条目上限为 maxEntries 的引擎（maxEntries<=0 表示不限）。
func New(maxEntries int) *Engine {
	return &Engine{a: dagg.New(maxEntries)}
}

// Feed 喂入一批有序变更，返回该批的变更日志；任一条被拒则整批不生效。
func (e *Engine) Feed(batch []Change) ([]Out, error) {
	return e.a.Feed(batch)
}

// View 返回下游应用全部变更日志后的物化视图：Group → distinct 数（0 的组不出现）。
func (e *Engine) View() map[string]int {
	return e.a.View()
}

// SelfCheck 对一组内置批次序列核验四条不变量，通过返回 nil。
func (e *Engine) SelfCheck() error {
	return e.a.SelfCheck()
}

// ScalingOK 只回布尔判定：单条变更批的检查条数不随组内规模增长（计数器数值不导出）。
func (e *Engine) ScalingOK() bool {
	return e.a.ScalingOK()
}
