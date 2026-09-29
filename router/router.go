package router

import (
	"sort"
	"sync"
)

// Event 是按分区投递、带分区内位点的变更事件。
// 位点从 0 开始，在分区内连续递增；Value 必须非负。
type Event struct {
	Partition int
	Offset    int64
	Value     int64
}

// PartitionStats 是单个分区的累计统计量。
type PartitionStats struct {
	Count int64
	Sum   int64
}

// Stats 是路由器某一时刻的完整统计快照。
type Stats struct {
	// Partitions 与分区号一一对应，Count/Sum 均单调不减。
	Partitions []PartitionStats
	// TotalSum 是所有分区已接受事件值的全局总和。
	TotalSum int64
	// Watermark 是所有分区都已对齐到的最小前缀长度（最小已接受事件数）。
	Watermark int64
	// CommittedSum 是水位前缀（各分区前 Watermark 个事件）的值之和。
	CommittedSum int64
}

// Router 是分区保序路由器。
type Router struct {
	mu sync.RWMutex

	// partitions[p].values[k] 即分区 p 位点 k 的事件值，
	// 只追加、不改写，因此所有统计量随喂入单调不减。
	partitions []partitionState
	totalSum   int64
}

type partitionState struct {
	values []int64
	sum    int64
}

// New 创建一个拥有 partitionCount 个分区（分区号 0..partitionCount-1）的路由器。
func New(partitionCount int) (*Router, error) {
	if partitionCount <= 0 {
		return nil, ErrInvalidPartitionCount
	}
	return &Router{partitions: make([]partitionState, partitionCount)}, nil
}

// Feed 原子地接受一个或一批事件；任一事件非法则整体拒绝，状态不变。
// 同一批事件的排列顺序不影响最终结果。
func (r *Router) Feed(events ...Event) error {
	// 携带原始下标，便于错误信息定位调用方批次中的位置。
	type indexed struct {
		idx int
		e   Event
	}
	ordered := make([]indexed, len(events))
	for i, e := range events {
		ordered[i] = indexed{idx: i, e: e}
	}
	// 按 (分区, 位点, 原始下标) 排序：
	// 使批次内事件的接受结果与其传入排列无关，且拒绝原因可复现。
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].e, ordered[j].e
		if a.Partition != b.Partition {
			return a.Partition < b.Partition
		}
		if a.Offset != b.Offset {
			return a.Offset < b.Offset
		}
		return ordered[i].idx < ordered[j].idx
	})

	r.mu.Lock()
	defer r.mu.Unlock()

	// 第一阶段：仅校验，不改动任何状态。
	// expected[p] 是分区 p 下一个被允许的位点。
	expected := make([]int64, len(r.partitions))
	for p := range r.partitions {
		expected[p] = int64(len(r.partitions[p].values))
	}
	for _, item := range ordered {
		e := item.e
		if e.Partition < 0 || e.Partition >= len(r.partitions) {
			return &FeedError{Index: item.idx, Event: e, Cause: ErrPartitionOutOfRange}
		}
		if e.Offset != expected[e.Partition] {
			return &FeedError{Index: item.idx, Event: e, Cause: ErrOffsetDiscontinuous}
		}
		if e.Value < 0 {
			return &FeedError{Index: item.idx, Event: e, Cause: ErrNegativeValue}
		}
		expected[e.Partition]++
	}

	// 第二阶段：全部合法后才整体提交，失败路径绝不触碰状态。
	for _, item := range ordered {
		e := item.e
		p := &r.partitions[e.Partition]
		p.values = append(p.values, e.Value)
		p.sum += e.Value
		r.totalSum += e.Value
	}
	return nil
}

// Stats 返回当前全部统计量的深拷贝快照，可与喂入并发调用。
func (r *Router) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := Stats{
		Partitions: make([]PartitionStats, len(r.partitions)),
	}
	watermark := int64(-1)
	for p := range r.partitions {
		count := int64(len(r.partitions[p].values))
		stats.Partitions[p] = PartitionStats{Count: count, Sum: r.partitions[p].sum}
		stats.TotalSum += r.partitions[p].sum
		if watermark < 0 || count < watermark {
			watermark = count
		}
	}
	if watermark < 0 {
		watermark = 0
	}
	stats.Watermark = watermark
	for p := range r.partitions {
		stats.CommittedSum += prefixSum(r.partitions[p].values, watermark)
	}
	return stats
}

// Snapshot 是 Stats 的语义别名。
func (r *Router) Snapshot() Stats {
	return r.Stats()
}

// prefixSum 返回 values 前 n 个元素之和（n 超出长度时取全部）。
func prefixSum(values []int64, n int64) int64 {
	if n > int64(len(values)) {
		n = int64(len(values))
	}
	var sum int64
	for _, v := range values[:n] {
		sum += v
	}
	return sum
}
