package ontology

import "sort"

// watermarkEntry 记录某一权限等级最近一次已生效写入的版本号。
type watermarkEntry struct {
	priority   Priority
	maxVersion int64
}

// priorityWatermark 按权限等级维护已生效写入的最高版本水位线。
// 其空间与查询开销只随不同权限等级的数量 L 增长，
// 与并发竞争同一实例的动作总数无关。
type priorityWatermark struct {
	entries   []watermarkEntry // 按 priority 升序
	suffixMax []int64          // suffixMax[i] = max(entries[i:].maxVersion)
}

// record 登记一次已生效写入。
func (w *priorityWatermark) record(p Priority, v int64) {
	i := sort.Search(len(w.entries), func(i int) bool {
		return w.entries[i].priority >= p
	})
	switch {
	case i < len(w.entries) && w.entries[i].priority == p:
		if v > w.entries[i].maxVersion {
			w.entries[i].maxVersion = v
			w.rebuildSuffix()
		}
	default:
		w.entries = append(w.entries, watermarkEntry{})
		copy(w.entries[i+1:], w.entries[i:])
		w.entries[i] = watermarkEntry{priority: p, maxVersion: v}
		w.suffixMax = append(w.suffixMax, 0)
		w.rebuildSuffix()
	}
}

// preempts 判定：是否存在已生效写入 W 满足
// W.priority > p 且 W.version > baseline。
// 时间复杂度 O(log L)，L 为不同权限等级数量。
func (w *priorityWatermark) preempts(p Priority, baseline int64) bool {
	i := sort.Search(len(w.entries), func(i int) bool {
		return w.entries[i].priority > p
	})
	return i < len(w.entries) && w.suffixMax[i] > baseline
}

// rebuildSuffix 重算全部 suffixMax（entries[i] 的更新会影响所有 j <= i）。
func (w *priorityWatermark) rebuildSuffix() {
	if len(w.suffixMax) != len(w.entries) {
		w.suffixMax = make([]int64, len(w.entries))
	}
	for j := len(w.entries) - 1; j >= 0; j-- {
		m := w.entries[j].maxVersion
		if j+1 < len(w.entries) && w.suffixMax[j+1] > m {
			m = w.suffixMax[j+1]
		}
		w.suffixMax[j] = m
	}
}

// levels 返回当前追踪的不同权限等级数量（用于开销证据）。
func (w *priorityWatermark) levels() int {
	return len(w.entries)
}
