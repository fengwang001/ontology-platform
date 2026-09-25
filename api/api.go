// Package api 是物化视图的对外门面，只暴露最小操作集。
package api

import "ontology/view"

// View 对外句柄，内部委托给 view.View。
type View struct {
	v *view.View
}

// New 返回一张空视图（cur = {Seq:0, Cells:{}}）。
func New() *View {
	return &View{v: view.New()}
}

// Read 返回当前一致快照的 (seq, cells)；cells 不可变，调用方不得修改。
func (v *View) Read() (seq int64, cells map[string]string) {
	return v.v.Read()
}

// StartRefresh 开始一轮重建：把当前快照拷贝为暂存。
func (v *View) StartRefresh() error {
	return v.v.StartRefresh()
}

// Stage 把一条写放进暂存，Commit 前对 Read 不可见。
func (v *View) Stage(k, val string) error {
	return v.v.Stage(k, val)
}

// Commit 固化暂存并原子切换到新版本（Seq+1）。
func (v *View) Commit() error {
	return v.v.Commit()
}

// Abort 丢弃暂存，旧视图完整回退。
func (v *View) Abort() error {
	return v.v.Abort()
}

// SelfCheck 对内置操作序列核验四条不变量与 O(1) Commit。
func (v *View) SelfCheck() error {
	return view.SelfCheck()
}
