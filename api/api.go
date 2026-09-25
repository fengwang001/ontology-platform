// Package api 是分组多列精确 distinct 计数的对外门面，依赖 dd，不反向依赖。
package api

import "ontology/dd"

// Counter 维护按 Key 分组的当前活跃行 (Col1,Col2) 元组的精确去重计数，
// 状态只在进程内存、仅用标准库。零值不可用，请用 New 创建。
type Counter struct {
	e *dd.Engine
}

// New 返回一个空 Counter。
func New() *Counter { return &Counter{e: dd.NewEngine()} }

// Upsert 新增一行；rowID 已存在时先撤回旧元组再写入新元组。
// rowID 非正、key 为空时整体失败且不改变任何状态。
func (c *Counter) Upsert(rowID int, key, col1 string, col2 int) error {
	return c.e.Upsert(rowID, key, col1, col2)
}

// Delete 撤回 rowID 当前元组并移除该行；rowID 不存在时整体失败、不留痕。
func (c *Counter) Delete(rowID int) error { return c.e.Delete(rowID) }

// Distinct 返回 key 分组当前活跃行按 (Col1,Col2) 去重后的元组个数。
func (c *Counter) Distinct(key string) int { return c.e.Distinct(key) }

// Total 返回所有分组 distinct 计数之和。
func (c *Counter) Total() int { return c.e.Total() }

// SelfCheck 对内置操作序列核验四条不变量（与批量重算一致、计数精确、
// 撤回对称、检查元组个数不随分组规模增长），通过返回 nil。
func (c *Counter) SelfCheck() error { return c.e.SelfCheck() }

// 哨兵错误原样透出，保证调用方可用 errors.Is 判定且三类互不相同。
var (
	ErrRowNotFound  = dd.ErrRowNotFound
	ErrEmptyKey     = dd.ErrEmptyKey
	ErrInvalidRowID = dd.ErrInvalidRowID
)
