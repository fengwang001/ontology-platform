// Package router 实现分区保序路由器。
package router

import (
	"errors"
	"fmt"
	"sync"
)

// RejectReason 表示事件被拒绝的具体原因，彼此可区分。
type RejectReason string

const (
	// ReasonPartitionOutOfRange 分区号不在 [0, PartitionCount) 内。
	ReasonPartitionOutOfRange RejectReason = "partition out of range"
	// ReasonOffsetGap 位点不连续：既不是期望的下一位点，也不是已接受位点的重复。
	ReasonOffsetGap RejectReason = "offset gap or out of order"
	// ReasonNegativeValue 事件携带的值为负数。
	ReasonNegativeValue RejectReason = "negative value"
	// ReasonEmptyBatch 空批次。
	ReasonEmptyBatch RejectReason = "empty batch"
)

// RejectError 携带可区分的拒绝原因与现场信息。
type RejectError struct {
	Reason RejectReason
	// Partition 触发拒绝的分区号（越界时为原始分区号）。
	Partition int
	// Offset 触发拒绝的位点。
	Offset int64
	// Want 该分区当前期望的下一位点；位点错误时有意义。
	Want int64
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("router: reject event (partition=%d offset=%d): %s (want offset=%d)",
		e.Partition, e.Offset, e.Reason, e.Want)
}

// Event 是按分区投递、带分区内位点的变更事件。
type Event struct {
	Partition int
	Offset    int64
	Value     int64
}

// PartitionStats 是单个分区的单调统计量。
type PartitionStats struct {
	Count int64
	Sum   int64
}

// Snapshot 是某一时刻路由器全部状态的不可变拷贝。
type Snapshot struct {
	Partitions   []PartitionStats
	GlobalSum    int64
	Watermark    int64
	CommittedSum int64
}

// Router 是分区保序路由器。零值不可用，必须用 New 构造。
type Router struct {
	mu       sync.RWMutex
	parts    []partState
	accepted [][]int64 // accepted[p][i] = 分区 p 位点 i 的值，用于提交前缀和与重算对照

	globalSum    int64
	watermark    int64 // 所有分区都已对齐到的最小前缀长度
	committedSum int64 // 位点区间 [0, watermark) 内所有分区值的总和
}

type partState struct {
	next  int64 // 期望的下一位点（也等于已接受条数）
	count int64
	sum   int64
}

// New 创建一个拥有 partitionCount 个分区的路由器。
func New(partitionCount int) (*Router, error) {
	if partitionCount <= 0 {
		return nil, fmt.Errorf("router: partitionCount must be positive, got %d", partitionCount)
	}
	r := &Router{
		parts:    make([]partState, partitionCount),
		accepted: make([][]int64, partitionCount),
	}
	return r, nil
}

// Feed 原子地接受单个事件；任何拒绝都不改变路由器状态。
func (r *Router) Feed(e Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.validate(e); err != nil {
		return err
	}

	r.apply(e)
	r.advanceWatermark()
	return nil
}

// FeedBatch 原子地接受一批事件：全部成功或全部拒绝。
func (r *Router) FeedBatch(events []Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(events) == 0 {
		return &RejectError{Reason: ReasonEmptyBatch}
	}

	// 阶段一：按批次内顺序逐条校验，任一失败则整批不落地。
	// 用影子状态模拟批次内各分区位点的推进，不触碰真实状态。
	shadowNext := make([]int64, len(r.parts))
	for p := range shadowNext {
		shadowNext[p] = r.parts[p].next
	}
	for _, e := range events {
		if err := r.validateWith(e, shadowNext); err != nil {
			return err
		}
		shadowNext[e.Partition]++
	}

	// 阶段二：全部合法，按同一顺序应用；此时不可能再失败。
	for _, e := range events {
		r.apply(e)
	}
	r.advanceWatermark()
	return nil
}

// Snapshot 返回全部统计量的一致拷贝，可与写入并发调用。
func (r *Router) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snap := Snapshot{
		Partitions:   make([]PartitionStats, len(r.parts)),
		GlobalSum:    r.globalSum,
		Watermark:    r.watermark,
		CommittedSum: r.committedSum,
	}
	for i, p := range r.parts {
		snap.Partitions[i] = PartitionStats{Count: p.count, Sum: p.sum}
	}
	return snap
}

// Check 执行自检：校验内部不变量，发现破坏时返回错误。
func (r *Router) Check() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var recomputedGlobal int64
	var minNext int64 = -1
	for p := range r.parts {
		st := &r.parts[p]
		if st.next != st.count {
			return fmt.Errorf("router self-check: partition %d next=%d != count=%d", p, st.next, st.count)
		}
		if int64(len(r.accepted[p])) != st.count {
			return fmt.Errorf("router self-check: partition %d accepted buffer len=%d != count=%d",
				p, len(r.accepted[p]), st.count)
		}
		var partSum int64
		for _, v := range r.accepted[p] {
			partSum += v
		}
		if partSum != st.sum {
			return fmt.Errorf("router self-check: partition %d buffered sum=%d != stats sum=%d",
				p, partSum, st.sum)
		}
		recomputedGlobal += partSum
		if minNext < 0 || st.next < minNext {
			minNext = st.next
		}
	}
	if minNext < 0 {
		minNext = 0
	}
	if r.globalSum != recomputedGlobal {
		return fmt.Errorf("router self-check: globalSum=%d != recomputed=%d", r.globalSum, recomputedGlobal)
	}
	if r.watermark != minNext {
		return fmt.Errorf("router self-check: watermark=%d != min partition prefix=%d", r.watermark, minNext)
	}

	var recomputedCommitted int64
	for p := range r.parts {
		for i := int64(0); i < minNext; i++ {
			recomputedCommitted += r.accepted[p][i]
		}
	}
	if r.committedSum != recomputedCommitted {
		return fmt.Errorf("router self-check: committedSum=%d != recomputed=%d",
			r.committedSum, recomputedCommitted)
	}
	return nil
}

// PartitionCount 返回分区数。
func (r *Router) PartitionCount() int {
	return len(r.parts)
}

// validate 基于真实状态校验单个事件。
func (r *Router) validate(e Event) error {
	return r.validateWith(e, nil)
}

// validateWith 校验事件；shadowNext 非空时以影子位点为准（用于批次原子校验）。
// 校验顺序固定为：分区越界 -> 位点不连续 -> 值为负，保证错误原因稳定可区分。
func (r *Router) validateWith(e Event, shadowNext []int64) error {
	if e.Partition < 0 || e.Partition >= len(r.parts) {
		return &RejectError{
			Reason:    ReasonPartitionOutOfRange,
			Partition: e.Partition,
			Offset:    e.Offset,
			Want:      -1,
		}
	}

	want := r.parts[e.Partition].next
	if shadowNext != nil {
		want = shadowNext[e.Partition]
	}
	if e.Offset != want {
		return &RejectError{
			Reason:    ReasonOffsetGap,
			Partition: e.Partition,
			Offset:    e.Offset,
			Want:      want,
		}
	}
	if e.Value < 0 {
		return &RejectError{
			Reason:    ReasonNegativeValue,
			Partition: e.Partition,
			Offset:    e.Offset,
			Want:      want,
		}
	}
	return nil
}

// apply 将已通过校验的事件落地到分区缓冲与统计量。调用方必须持锁。
func (r *Router) apply(e Event) {
	st := &r.parts[e.Partition]
	r.accepted[e.Partition] = append(r.accepted[e.Partition], e.Value)
	st.next++
	st.count++
	st.sum += e.Value
	r.globalSum += e.Value
}

// advanceWatermark 依据所有分区已对齐的最小前缀长度推进水位，
// 并把新纳入提交前缀的位点值累加进 committedSum。调用方必须持锁。
func (r *Router) advanceWatermark() {
	minNext := r.parts[0].next
	for _, st := range r.parts[1:] {
		if st.next < minNext {
			minNext = st.next
		}
	}
	for minNext > r.watermark {
		for p := range r.parts {
			r.committedSum += r.accepted[p][r.watermark]
		}
		r.watermark++
	}
}

// AsRejectError 从 error 中提取拒绝信息；不是拒绝错误时返回 nil。
func AsRejectError(err error) *RejectError {
	var reject *RejectError
	if errors.As(err, &reject) {
		return reject
	}
	return nil
}
