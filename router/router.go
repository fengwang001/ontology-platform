// Package router 实现分区保序路由器。
//
// 每个分区的变更事件必须携带从 0 开始、连续递增、无空洞的分区内位点；
// 同分区严格按位点顺序接受，跨分区允许任意交错。路由器在接受事件的同时
// 维护各分区计数/和值、全局总和，以及由所有分区共同对齐的最小前缀长度
// 决定的水位（watermark）与已提交前缀和（committed prefix sum）。
package router

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的拒绝原因。调用方可用 errors.Is 判定。
var (
	// ErrInvalidPartition 表示分区号越界（partition < 0 或 >= 分区数）。
	ErrInvalidPartition = errors.New("router: partition out of range")
	// ErrDiscontinuousOffset 表示位点不连续：必须从 0 开始且严格 +1。
	ErrDiscontinuousOffset = errors.New("router: offset not contiguous from zero")
	// ErrNegativeValue 表示事件携带的值为负。
	ErrNegativeValue = errors.New("router: negative value")
)

// Event 是一条按分区投递的变更事件。
type Event struct {
	Partition int
	Offset    int64
	Value     int64
}

// PartitionStats 是单个分区的统计快照。
type PartitionStats struct {
	// Accepted 为该分区已接受的事件数，同时也是下一个期望位点。
	Accepted int64
	// Sum 为该分区所有已接受事件值之和。
	Sum int64
}

// Stats 是路由器的不可变统计快照。
type Stats struct {
	Partitions   []PartitionStats
	TotalSum     int64
	Watermark    int64
	CommittedSum int64
}

// RejectError 携带一次整批拒绝的具体上下文；Cause 恒为 ErrInvalidPartition、
// ErrDiscontinuousOffset 或 ErrNegativeValue 之一，可用 errors.Is 区分原因。
type RejectError struct {
	Cause     error
	Partition int
	Want      int64 // 期望位点（仅位点不连续时有意义）
	Got       int64 // 实际位点（仅位点不连续时有意义）
}

func (e *RejectError) Error() string {
	switch {
	case errors.Is(e.Cause, ErrInvalidPartition):
		return fmt.Sprintf("%v: partition=%d", e.Cause, e.Partition)
	case errors.Is(e.Cause, ErrDiscontinuousOffset):
		return fmt.Sprintf("%v: partition=%d want=%d got=%d", e.Cause, e.Partition, e.Want, e.Got)
	default:
		return fmt.Sprintf("%v: partition=%d", e.Cause, e.Partition)
	}
}

func (e *RejectError) Unwrap() error { return e.Cause }

// Router 是并发安全的分区保序路由器。
type Router struct {
	mu sync.RWMutex

	counts []int64   // 各分区已接受事件数（= 下一个期望位点）
	sums   []int64   // 各分区已接受事件值之和
	values [][]int64 // 各分区按位点保存的值，用于水位推进时重放

	totalSum     int64 // 全局总和：所有分区 Sum 之和
	watermark    int64 // 所有分区都已对齐到的最小前缀长度
	committedSum int64 // 水位以下（位点 < watermark）所有分区事件值之和
}

// New 创建一个具有 partitionCount 个分区的路由器。partitionCount 必须为正。
func New(partitionCount int) *Router {
	if partitionCount <= 0 {
		panic(fmt.Sprintf("router: partitionCount must be positive, got %d", partitionCount))
	}
	return &Router{
		counts: make([]int64, partitionCount),
		sums:   make([]int64, partitionCount),
		values: make([][]int64, partitionCount),
	}
}

type groupedEvent struct {
	offset int64
	value  int64
}

// Ingest 原子地接受一批事件：任一事件非法则整批拒绝、状态不变。
//
// 批内同一分区的事件允许以任意顺序出现：路由器会按位点排序后要求它们与该
// 分区当前位点首尾相接（从当前已接受计数开始连续、无重复）。因此同一批事件
// 无论跨分区如何交错、同分区事件在批次内如何排列，终态唯一且可复现。
func (r *Router) Ingest(events []Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := int64(len(r.counts))
	grouped := make(map[int64][]groupedEvent, len(events))
	order := make([]int64, 0, len(events))

	// 阶段一：纯校验，不触碰任何持久状态；任何失败直接返回，状态零改动。
	for _, ev := range events {
		if ev.Partition < 0 || int64(ev.Partition) >= n {
			return &RejectError{Cause: ErrInvalidPartition, Partition: ev.Partition}
		}
		if ev.Value < 0 {
			return &RejectError{Cause: ErrNegativeValue, Partition: ev.Partition, Got: ev.Offset}
		}
		p := int64(ev.Partition)
		if _, ok := grouped[p]; !ok {
			order = append(order, p)
		}
		grouped[p] = append(grouped[p], groupedEvent{offset: ev.Offset, value: ev.Value})
	}

	for _, p := range order {
		items := grouped[p]
		sort.Slice(items, func(i, j int) bool { return items[i].offset < items[j].offset })
		want := r.counts[p]
		for _, it := range items {
			if it.offset != want {
				return &RejectError{
					Cause:     ErrDiscontinuousOffset,
					Partition: int(p),
					Want:      want,
					Got:       it.offset,
				}
			}
			want++
		}
	}

	// 阶段二：全部合法后才提交，校验失败永远不会走到这里。
	for _, p := range order {
		items := grouped[p]
		sort.Slice(items, func(i, j int) bool { return items[i].offset < items[j].offset })
		batchSum := int64(0)
		for _, it := range items {
			r.values[p] = append(r.values[p], it.value)
			batchSum += it.value
		}
		r.sums[p] += batchSum
		r.totalSum += batchSum
		r.counts[p] += int64(len(items))
	}

	// 推进水位：水位 = 所有分区已接受计数的最小值（最小公共前缀长度）。
	newWatermark := r.counts[0]
	for _, c := range r.counts[1:] {
		if c < newWatermark {
			newWatermark = c
		}
	}
	for newWatermark > r.watermark {
		for p := range r.values {
			r.committedSum += r.values[p][r.watermark]
		}
		r.watermark++
	}
	return nil
}

// Stats 返回当前统计的一份不可变快照（切片为独立拷贝）。
func (r *Router) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	partitions := make([]PartitionStats, len(r.counts))
	for p := range r.counts {
		partitions[p] = PartitionStats{Accepted: r.counts[p], Sum: r.sums[p]}
	}
	return Stats{
		Partitions:   partitions,
		TotalSum:     r.totalSum,
		Watermark:    r.watermark,
		CommittedSum: r.committedSum,
	}
}

// Verify 执行内部不变量自检，发现破坏时返回描述性错误。
func (r *Router) Verify() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n := len(r.counts)
	if len(r.sums) != n || len(r.values) != n {
		return fmt.Errorf("router: internal slice length mismatch counts=%d sums=%d values=%d",
			n, len(r.sums), len(r.values))
	}

	var recomputedTotal int64
	minCount := r.counts[0]
	for p := 0; p < n; p++ {
		c := r.counts[p]
		if c != int64(len(r.values[p])) {
			return fmt.Errorf("router: partition %d count=%d but buffered=%d", p, c, len(r.values[p]))
		}
		var sum int64
		for _, v := range r.values[p] {
			if v < 0 {
				return fmt.Errorf("router: partition %d buffered negative value %d", p, v)
			}
			sum += v
		}
		if sum != r.sums[p] {
			return fmt.Errorf("router: partition %d sum field=%d recomputed=%d", p, r.sums[p], sum)
		}
		recomputedTotal += sum
		if c < minCount {
			minCount = c
		}
	}
	if recomputedTotal != r.totalSum {
		return fmt.Errorf("router: totalSum field=%d recomputed=%d", r.totalSum, recomputedTotal)
	}
	if minCount != r.watermark {
		return fmt.Errorf("router: watermark field=%d recomputed min prefix=%d", r.watermark, minCount)
	}

	var recomputedCommitted int64
	for p := 0; p < n; p++ {
		for off := int64(0); off < r.watermark; off++ {
			recomputedCommitted += r.values[p][off]
		}
	}
	if recomputedCommitted != r.committedSum {
		return fmt.Errorf("router: committedSum field=%d recomputed=%d", r.committedSum, recomputedCommitted)
	}
	return nil
}
