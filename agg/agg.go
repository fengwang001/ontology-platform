// Package agg 按归一化键维护各组计数：负值拒绝、归零即删、枚举排序。
package agg

import (
	"sort"

	"ontology/grp"
)

// Agg 是各组计数的增量视图。计数为 0 的组不存于 map（归零即删）。
type Agg struct {
	counts map[grp.Key]int64
	// probes 记录最近一次 Apply 定位分组时检查过的组个数。
	// 非导出，不出现在任何公开接口；仅供包内测试断言定位是 O(1) 哈希定位。
	probes int
}

// New 返回空视图。
func New() *Agg { return &Agg{counts: make(map[grp.Key]int64)} }

// Apply 尝试对键 k 施加增量 d。若结果会为负则拒绝并返回 false（状态不变）；
// 否则应用并返回 true，结果为 0 时删除该组。
func (a *Agg) Apply(k grp.Key, d int64) bool {
	a.probes = 1 // map 按内部键哈希定位，只检查目标组一个
	n := a.counts[k] + d
	if n < 0 {
		return false
	}
	if n == 0 {
		delete(a.counts, k)
	} else {
		a.counts[k] = n
	}
	return true
}

// Count 返回键 k 的当前计数；不存在的组返回 0。
func (a *Agg) Count(k grp.Key) int64 { return a.counts[k] }

// Keys 返回当前存在（计数非 0）的键，按 NULL 最前、其余字典序排列。
func (a *Agg) Keys() []grp.Key {
	ks := make([]grp.Key, 0, len(a.counts))
	for k := range a.counts {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].Less(ks[j]) })
	return ks
}

// Clone 返回深拷贝，供整批试算（失败不留痕）使用。
func (a *Agg) Clone() *Agg {
	c := &Agg{counts: make(map[grp.Key]int64, len(a.counts))}
	for k, v := range a.counts {
		c.counts[k] = v
	}
	return c
}
