// Package agg 按归一化键维护各组计数：归零即删、枚举排序、整批原子应用。
package agg

import (
	"errors"
	"sort"

	"ontology/grp"
)

// ErrNegative 哨兵错误：某条增量会把组计数驱动到负数。
var ErrNegative = errors.New("agg: 计数不可为负")

// Map 是「组 → 计数」的物化视图，只存计数非 0 的组。
// 不加锁；并发控制由上层 api 负责。
type Map struct {
	counts map[grp.Key]int64
	// lastChecks 记录最近一次定位分组时检查过的组个数。
	// 非导出，不出现在任何公开接口的数值里。
	lastChecks int
}

// New 返回空视图。
func New() *Map { return &Map{counts: map[grp.Key]int64{}} }

// locate 按内部键哈希定位，一次只检查 1 个组（非整表扫描）。
func (m *Map) locate(k grp.Key) int64 {
	m.lastChecks = 1
	return m.counts[k]
}

// ApplyBatch 把「键 → 总增量」整批原子应用：
// 先整体校验，任一键会变负则返回 ErrNegative 且一个键都不改。
func (m *Map) ApplyBatch(deltas map[grp.Key]int64) error {
	for k, d := range deltas {
		if m.locate(k)+d < 0 {
			return ErrNegative
		}
	}
	for k, d := range deltas {
		switch nv := m.counts[k] + d; {
		case nv == 0:
			delete(m.counts, k) // 归零即删，组回到不存在
		default:
			m.counts[k] = nv
		}
	}
	return nil
}

// Count 返回某组当前计数；不存在的组为 0。
func (m *Map) Count(k grp.Key) int64 { return m.counts[k] }

// Keys 按 NULL、空串、字典序枚举所有计数非 0 的组。
func (m *Map) Keys() []grp.Key {
	ks := make([]grp.Key, 0, len(m.counts))
	for k := range m.counts {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return grp.Less(ks[i], ks[j]) })
	return ks
}

// LastLocateChecksAtMost 只回答「最近一次定位的检查个数是否不超过 n」，
// 不暴露计数器数值本身。
func (m *Map) LastLocateChecksAtMost(n int) bool { return m.lastChecks <= n }
