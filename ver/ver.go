// Package ver 管理单个物化版本的引用计数与归零回收判定，不依赖其他包。
//
// 本包自身不加锁：引用计数的所有变更都由 store 在其互斥锁内串行调用，
// ver 只负责「加一 / 减一判零」这一 O(1) 判定。
package ver

// Version 是一个带引用计数的不可变版本。零值不可用，必须经 New 创建。
type Version struct {
	value int
	refs  int
	alive bool
}

// New 创建一个 value 给定、refs=1（创建者持有的首份引用）的存活版本。
func New(value int) *Version {
	return &Version{value: value, refs: 1, alive: true}
}

// AddRef 增加一次引用。调用方必须保证版本仍存活且持有外层互斥锁。
func (v *Version) AddRef() { v.refs++ }

// ReleaseOne 减少一次引用并检查这一个版本：refs 归零时立即标记为已回收（O(1)），
// 返回 true 表示调用方应当把它从存活集合移除。绝不扫描其他版本。
func (v *Version) ReleaseOne() bool {
	v.refs--
	if v.refs == 0 {
		v.alive = false
		return true
	}
	return false
}

// Refs 返回当前引用计数（已回收版本恒为 0）。
func (v *Version) Refs() int { return v.refs }

// Value 返回发布时写入的值。
func (v *Version) Value() int { return v.value }

// Alive 报告版本是否仍在存活集合中。
func (v *Version) Alive() bool { return v.alive }
