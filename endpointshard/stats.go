package endpointshard

import "sync/atomic"

// Stats 是可验证性能证据的操作计数器。
//
// 计数单位为索引结构（有序集合）的节点访问次数：
// 同步/调整只应访问与受影响端点、受影响分片相关的对数个节点，
// 查询只应访问与结果大小成正比的节点数。
type Stats struct {
	// IndexVisits 同步与分片大小调整过程中对索引结构的节点访问次数。
	IndexVisits atomic.Uint64
	// QueryVisits 查询过程中对索引结构的节点访问次数。
	QueryVisits atomic.Uint64
}

// StatsSnapshot 是 Stats 在某时刻的值快照。
type StatsSnapshot struct {
	IndexVisits uint64
	QueryVisits uint64
}

func (s *Stats) snapshot() StatsSnapshot {
	return StatsSnapshot{
		IndexVisits: s.IndexVisits.Load(),
		QueryVisits: s.QueryVisits.Load(),
	}
}

func (s *Stats) reset() {
	s.IndexVisits.Store(0)
	s.QueryVisits.Store(0)
}
