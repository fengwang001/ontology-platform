// Package script 提供迁移脚本的登记表：版本号、校验和与是否带回退动作。
package script

import "sort"

// 脚本版本号合法范围 [MinVer, MaxVer]。
const (
	MinVer = 1
	MaxVer = 1_000_000
)

// Script 是一个已登记的迁移脚本。
type Script struct {
	Ver     uint32
	Sum     uint64
	HasUndo bool
}

// Valid 报告脚本参数是否合法。
func (s Script) Valid() bool {
	return s.Ver >= MinVer && s.Ver <= MaxVer
}

// Registry 保存 ver -> Script 的登记表，同 ver 重复登记会覆盖。
// Registry 自身不加锁，并发串行化由调用方（runner 引擎）负责。
type Registry struct {
	m map[uint32]Script
}

// NewRegistry 返回空登记表。
func NewRegistry() *Registry {
	return &Registry{m: make(map[uint32]Script)}
}

// Upsert 覆盖式登记：同 ver 已登记则整体替换（模拟脚本文件被改动）。
func (r *Registry) Upsert(s Script) {
	r.m[s.Ver] = s
}

// Get 返回 ver 的登记值与是否存在。
func (r *Registry) Get(ver uint32) (Script, bool) {
	s, ok := r.m[ver]
	return s, ok
}

// Snapshot 返回按 ver 升序排列的全部登记脚本（深拷贝）。
func (r *Registry) Snapshot() []Script {
	out := make([]Script, 0, len(r.m))
	for _, s := range r.m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ver < out[j].Ver })
	return out
}
