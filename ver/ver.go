// Package ver 实现单版本的引用计数与回收判定，不依赖其他包。
package ver

// Version 是一个物化状态版本：不可变的 Value 加引用计数。
type Version struct {
	Value int
	refs  int
}

// New 创建 refs=1 的版本（store 引用）。
func New(value int) *Version { return &Version{Value: value, refs: 1} }

// Inc 增加一份引用。
func (v *Version) Inc() { v.refs++ }

// Dec 减少一份引用；返回 true 表示 refs 归零，应立即回收。
func (v *Version) Dec() bool {
	v.refs--
	return v.refs == 0
}

// Refs 返回当前引用计数（仅供同进程内判定与自检）。
func (v *Version) Refs() int { return v.refs }
