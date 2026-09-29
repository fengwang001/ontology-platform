package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// Event 是一条按分区投递、带分区内位点的变更事件。
type Event struct {
	Partition int
	Offset    uint64
	Value     int64
}

// RejectReason 标识事件被整体拒绝的可区分原因。
type RejectReason string

const (
	ReasonPartitionOutOfRange RejectReason = "partition out of range"
	ReasonOffsetGap           RejectReason = "offset not contiguous"
	ReasonNegativeValue       RejectReason = "negative value"
)

var ErrRejected = errors.New("event rejected")

// RejectError 描述一次被拒绝的喂入及其原因。
type RejectError struct {
	Reason RejectReason
	Event  Event
	// Index 是 FeedBatch 中导致拒绝的事件下标；Feed 单条喂入时为 0。
	Index int
	// Want 是该分区当前期望的下一个位点；位点不连续时用于判定依据。
	Want uint64
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("rejected event %+v at batch index %d: %s (partition expects offset %d)",
		e.Event, e.Index, e.Reason, e.Want)
}

func (e *RejectError) Unwrap() error { return ErrRejected }

// Stats 是某一时刻路由器全部统计量的一致快照。
type Stats struct {
	PartitionCounts []int64
	PartitionSums   []int64
	GlobalSum       int64
	// Watermark 是所有分区都已对齐到的最小前缀长度，
	// 即 min_p(分区p已接受条数)。
	Watermark int64
	// CommittedSum 是各分区 [0, Watermark) 前缀之和的总和。
	CommittedSum int64
}

type partitionState struct {
	// accepted 是该分区已接受事件的条数；下一个事件位点必须等于该值。
	accepted uint64
	// values[i] 是位点 i 的事件值，offset 与下标一一对应。
	values []int64
	// prefix[i] 是 [0, i) 的值之和，prefix[0] == 0。
	prefix []int64
}

// Router 是分区保序路由器：分区内严格按位点连续接受，跨分区自由交错。
type Router struct {
	mu         sync.RWMutex
	partitions int
	states     []partitionState
}

// NewRouter 创建一个拥有 partitionCount 个分区的路由器。
func NewRouter(partitionCount int) (*Router, error) {
	if partitionCount <= 0 {
		return nil, fmt.Errorf("partitionCount must be positive, got %d", partitionCount)
	}
	r := &Router{
		partitions: partitionCount,
		states:     make([]partitionState, partitionCount),
	}
	for i := range r.states {
		r.states[i].prefix = []int64{0}
	}
	return r, nil
}

// Feed 接受一条事件；失败时不改变任何状态。
func (r *Router) Feed(e Event) *RejectError {
	if e.Partition < 0 || e.Partition >= r.partitions {
		return &RejectError{Reason: ReasonPartitionOutOfRange, Event: e}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateLocked(e, 0, r.states[e.Partition].accepted); err != nil {
		return err
	}
	r.appendLocked(e)
	return nil
}

// FeedBatch 原子地接受一批事件；任一非法则整批拒绝、状态不变。
// 批内事件可跨分区任意交错，但每个分区的子序列必须严格按位点连续。
func (r *Router) FeedBatch(events []Event) *RejectError {
	r.mu.Lock()
	defer r.mu.Unlock()

	// projected 模拟整批应用后每个分区的条数；失败即丢弃、不落任何状态。
	projected := make([]uint64, r.partitions)
	for i := range projected {
		projected[i] = r.states[i].accepted
	}
	for idx, e := range events {
		if e.Partition < 0 || e.Partition >= r.partitions {
			return &RejectError{Reason: ReasonPartitionOutOfRange, Event: e, Index: idx}
		}
		if err := r.validateLocked(e, idx, projected[e.Partition]); err != nil {
			return err
		}
		projected[e.Partition]++
	}
	for _, e := range events {
		r.appendLocked(e)
	}
	return nil
}

// Snapshot 返回当前全部统计量的一致快照。
// 返回的切片为独立拷贝，调用方修改不影响路由器内部状态。
func (r *Router) Snapshot() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLocked()
}

// SelfCheck 基于内部缓冲逐项重算并与快照比对，返回快照与判定结果。
func (r *Router) SelfCheck() (Stats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := r.snapshotLocked()
	var global int64
	var watermark uint64 = ^uint64(0)
	for p := 0; p < r.partitions; p++ {
		st := &r.states[p]
		if uint64(len(st.values)) != st.accepted ||
			uint64(len(st.prefix)) != st.accepted+1 ||
			st.prefix[0] != 0 {
			return stats, fmt.Errorf("partition %d internal buffers inconsistent", p)
		}
		var recomputed int64
		for i, v := range st.values {
			recomputed += v
			if st.prefix[i+1] != recomputed {
				return stats, fmt.Errorf("partition %d prefix sum mismatch at offset %d", p, i)
			}
		}
		if stats.PartitionCounts[p] != int64(st.accepted) ||
			stats.PartitionSums[p] != recomputed {
			return stats, fmt.Errorf("partition %d reported stats disagree with buffers", p)
		}
		global += recomputed
		if st.accepted < watermark {
			watermark = st.accepted
		}
	}
	if stats.GlobalSum != global {
		return stats, fmt.Errorf("global sum %d != recomputed %d", stats.GlobalSum, global)
	}
	if stats.Watermark != int64(watermark) {
		return stats, fmt.Errorf("watermark %d != min prefix %d", stats.Watermark, watermark)
	}
	var committed int64
	for p := 0; p < r.partitions; p++ {
		committed += r.states[p].prefix[watermark]
	}
	if stats.CommittedSum != committed {
		return stats, fmt.Errorf("committed sum %d != recomputed %d", stats.CommittedSum, committed)
	}
	return stats, nil
}

// validateLocked 在持锁状态下校验单条事件相对期望位点 want 是否合法。
func (r *Router) validateLocked(e Event, index int, want uint64) *RejectError {
	if e.Offset != want {
		return &RejectError{Reason: ReasonOffsetGap, Event: e, Index: index, Want: want}
	}
	if e.Value < 0 {
		return &RejectError{Reason: ReasonNegativeValue, Event: e, Index: index, Want: want}
	}
	return nil
}

func (r *Router) appendLocked(e Event) {
	st := &r.states[e.Partition]
	st.values = append(st.values, e.Value)
	st.prefix = append(st.prefix, st.prefix[st.accepted]+e.Value)
	st.accepted++
}

func (r *Router) snapshotLocked() Stats {
	stats := Stats{
		PartitionCounts: make([]int64, r.partitions),
		PartitionSums:   make([]int64, r.partitions),
	}
	var watermark uint64 = ^uint64(0)
	for p := 0; p < r.partitions; p++ {
		accepted := r.states[p].accepted
		stats.PartitionCounts[p] = int64(accepted)
		stats.PartitionSums[p] = r.states[p].prefix[accepted]
		stats.GlobalSum += stats.PartitionSums[p]
		if accepted < watermark {
			watermark = accepted
		}
	}
	stats.Watermark = int64(watermark)
	for p := 0; p < r.partitions; p++ {
		stats.CommittedSum += r.states[p].prefix[watermark]
	}
	return stats
}
