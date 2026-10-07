// Package bitemporal 负责双时态索引与历史重建。
//
// 查询复杂度：按 (系统时间, 业务时间) 双坐标定位版本的代价为 O(log V)
// （V 为该主键历史版本总数），与对象类型下的实例总数无关。
// 每次查询返回探测步数，供外部以不依赖全量重遍历的方式验证该上界。
package bitemporal

import (
	"sync"

	"ontology/core"
)

// Index 是双时态索引，按主键分桶，互不影响。
type Index struct {
	mu   sync.RWMutex
	keys map[core.Key]*keyIndex
}

// New 创建空索引。
func New() *Index {
	return &Index{keys: make(map[core.Key]*keyIndex)}
}

// Apply 将一条已提交版本纳入索引。必须按 Seq 递增顺序追加。
// 只有仲裁器已确认的版本才会到达这里；被拒绝的写入对索引不可见。
func (ix *Index) Apply(k core.Key, v core.Version) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ki := ix.keys[k]
	if ki == nil {
		ki = newKeyIndex()
		ix.keys[k] = ki
	}
	ki.apply(v)
}

// Query 按 (sysQ, bizQ) 双坐标定位可见版本，返回结果与探测步数。
//
// 语义：在「系统时间序号 <= sysQ」的全部版本中，业务时间区间覆盖
// bizQ 且系统时间最晚的那一条胜出；业务起点相等的多条记录都保留在
// 版本链中，但可见性由系统时间更晚者覆盖。主键从未写入返回
// NeverExisted，主键存在但坐标下无可见版本返回 NotVisible。
func (ix *Index) Query(k core.Key, sysQ uint64, bizQ int64) (core.QueryResult, int) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	ki := ix.keys[k]
	if ki == nil {
		return core.QueryResult{Visibility: core.NeverExisted}, 0
	}
	return ki.query(sysQ, bizQ)
}

// RebuildAsOf 重建 sysQ 系统时刻下该主键的完整可见时间线。
func (ix *Index) RebuildAsOf(k core.Key, sysQ uint64) []core.Interval {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	ki := ix.keys[k]
	if ki == nil {
		return nil
	}
	return ki.rebuildAsOf(sysQ)
}
