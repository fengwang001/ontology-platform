// Package api 是两阶段提交器对外的并发安全门面，依赖 txn，不反向依赖。
package api

import "ontology/txn"

// Coordinator 对外暴露 Apply/Commit/查询/恢复/自检。
type Coordinator struct {
	t *txn.Txn
}

// New 创建一个 C=0、store 空的提交器。
func New() *Coordinator { return &Coordinator{t: txn.New()} }

// Apply 阶段一：写副作用（幂等覆盖），不推进位点。
func (x *Coordinator) Apply(seq, eff int64) error { return x.t.Apply(seq, eff) }

// Commit 阶段二：把已提交位点推进到 seq。
func (x *Coordinator) Commit(seq int64) error { return x.t.Commit(seq) }

// Committed 返回已提交位点 C。
func (x *Coordinator) Committed() int64 { return x.t.Committed() }

// Pending 返回已写效果但位点未提交的序号，升序。
func (x *Coordinator) Pending() []int64 { return x.t.Pending() }

// Restart 模拟崩溃重启并恢复：持久化的 store 与 C 保留，在途序号随后用 Pending 取。
func (x *Coordinator) Restart() error {
	x.t.Restart()
	return nil
}

// SelfCheck 对内置操作序列核验四条不变量与 O(1) 复杂度。
func (x *Coordinator) SelfCheck() error { return x.t.SelfCheck() }
