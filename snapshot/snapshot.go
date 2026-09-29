// Package snapshot 提供并行对齐快照点导出器：多个分区独立投递事件，
// 对齐点取所有分区已投递事件数的最小值，快照按分区顺序拼接各分区
// 的前对齐点条事件，同键跨分区后写覆盖前写。
package snapshot

import (
	"errors"
	"fmt"
	"sync"
)

// 可区分的拒绝原因，配合 errors.Is 判定。
var (
	// ErrPartitionOutOfRange 表示分区号越界。
	ErrPartitionOutOfRange = errors.New("snapshot: partition out of range")
	// ErrEmptyKey 表示事件键为空。
	ErrEmptyKey = errors.New("snapshot: empty key")
	// ErrBackpressure 表示某分区超出对齐点的待定缓冲达到上限。
	ErrBackpressure = errors.New("snapshot: pending buffer limit exceeded")
)

// Event 是单个分区投递的一条事件，同分区内 Seq 从 1 开始连续递增。
type Event struct {
	Partition int
	Seq       int
	Key       string
	Value     []byte
}

// Exporter 并行对齐快照点导出器，所有方法均可并发调用。
type Exporter struct {
	mu           sync.RWMutex
	parts        [][]Event
	pendingLimit int
}

// NewExporter 创建拥有 numPartitions 个分区的导出器；
// pendingLimit 为每个分区超出对齐点的待定缓冲上限。
func NewExporter(numPartitions, pendingLimit int) (*Exporter, error) {
	if numPartitions <= 0 {
		return nil, fmt.Errorf("snapshot: numPartitions must be positive, got %d", numPartitions)
	}
	if pendingLimit < 0 {
		return nil, fmt.Errorf("snapshot: pendingLimit must be non-negative, got %d", pendingLimit)
	}
	return &Exporter{
		parts:        make([][]Event, numPartitions),
		pendingLimit: pendingLimit,
	}, nil
}

// Deliver 向指定分区投递一条事件。
// 分区号越界、空键、背压超限分别返回对应哨兵错误，且状态不变。
func (e *Exporter) Deliver(partition int, key string, value []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if partition < 0 || partition >= len(e.parts) {
		return fmt.Errorf("%w: partition %d, numPartitions %d", ErrPartitionOutOfRange, partition, len(e.parts))
	}
	if key == "" {
		return fmt.Errorf("%w: partition %d", ErrEmptyKey, partition)
	}
	// 背压判定：投递后该分区超出当前对齐点的待定缓冲条数不得超过上限。
	align := e.alignPointLocked()
	if len(e.parts[partition])+1-align > e.pendingLimit {
		return fmt.Errorf("%w: partition %d pending %d, limit %d",
			ErrBackpressure, partition, len(e.parts[partition])-align, e.pendingLimit)
	}
	v := make([]byte, len(value))
	copy(v, value)
	e.parts[partition] = append(e.parts[partition], Event{
		Partition: partition,
		Seq:       len(e.parts[partition]) + 1,
		Key:       key,
		Value:     v,
	})
	return nil
}

// Counts 返回各分区已投递事件数的快照。
func (e *Exporter) Counts() []int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	counts := make([]int, len(e.parts))
	for i, p := range e.parts {
		counts[i] = len(p)
	}
	return counts
}

// AlignPoint 返回当前对齐点，即所有分区已投递事件数的最小值。
func (e *Exporter) AlignPoint() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.alignPointLocked()
}

func (e *Exporter) alignPointLocked() int {
	align := len(e.parts[0])
	for _, p := range e.parts[1:] {
		if len(p) < align {
			align = len(p)
		}
	}
	return align
}

// Pending 返回指定分区超出当前对齐点的待定缓冲条数。
func (e *Exporter) Pending(partition int) (int, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if partition < 0 || partition >= len(e.parts) {
		return 0, fmt.Errorf("%w: partition %d, numPartitions %d", ErrPartitionOutOfRange, partition, len(e.parts))
	}
	return len(e.parts[partition]) - e.alignPointLocked(), nil
}

// Snapshot 是一次全局一致快照：对齐点 + 各分区前对齐点条事件按分区顺序拼接。
type Snapshot struct {
	AlignPoint int
	Events     []Event
}

// Snapshot 导出当前对齐点的一致快照。
func (e *Exporter) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	snap := e.snapshotLocked(e.alignPointLocked())
	for i := range snap.Events {
		v := make([]byte, len(snap.Events[i].Value))
		copy(v, snap.Events[i].Value)
		snap.Events[i].Value = v
	}
	return snap
}

// Materialize 将快照事件按顺序应用到键值视图，同键后写覆盖前写。
func (s Snapshot) Materialize() map[string][]byte {
	view := make(map[string][]byte, len(s.Events))
	for _, ev := range s.Events {
		v := make([]byte, len(ev.Value))
		copy(v, ev.Value)
		view[ev.Key] = v
	}
	return view
}

// SelfCheck 校验不变量：同分区序号连续、对齐点等于各分区事件数最小值、
// 任一快照内每个分区贡献条数恰等于对齐点。
func (e *Exporter) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for i, p := range e.parts {
		for j, ev := range p {
			if ev.Seq != j+1 {
				return fmt.Errorf("snapshot: partition %d seq discontinuity at index %d: seq %d", i, j, ev.Seq)
			}
			if ev.Partition != i {
				return fmt.Errorf("snapshot: partition %d event tagged partition %d", i, ev.Partition)
			}
		}
	}
	align := e.alignPointLocked()
	contrib := make([]int, len(e.parts))
	for _, ev := range e.snapshotLocked(align).Events {
		contrib[ev.Partition]++
	}
	for i, c := range contrib {
		if c != align {
			return fmt.Errorf("snapshot: partition %d contributes %d events, align point %d", i, c, align)
		}
	}
	return nil
}

func (e *Exporter) snapshotLocked(align int) Snapshot {
	snap := Snapshot{AlignPoint: align}
	for i, p := range e.parts {
		for _, ev := range p[:align] {
			ev.Partition = i
			snap.Events = append(snap.Events, ev)
		}
	}
	return snap
}
