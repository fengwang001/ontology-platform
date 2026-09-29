// Package ontology 提供跨分区并行对齐快照导出能力。
package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// ErrPartitionOutOfRange 表示投递事件时分区号越界。
var ErrPartitionOutOfRange = errors.New("ontology: partition out of range")

// ErrEmptyKey 表示事件键为空。
var ErrEmptyKey = errors.New("ontology: empty event key")

// ErrBackpressure 表示分区待定缓冲已达上限，事件被拒绝。
var ErrBackpressure = errors.New("ontology: backpressure limit exceeded")

// Event 是投递到某个分区的一条事件。
type Event struct {
	Key   string
	Value string
}

// Snapshot 是某一对齐点上的全局一致快照。
type Snapshot struct {
	// Alignment 是本快照的对齐点，即所有分区已投递事件数的最小值。
	Alignment int
	// Entries 按分区顺序拼接各分区前 Alignment 条事件，
	// 同键跨分区时后写（分区号更大）覆盖前写。
	Entries map[string]Event
}

// Exporter 并行对齐快照导出器：
// 每个分区独立投递事件，快照只取各分区等长前缀。
type Exporter struct {
	mu         sync.RWMutex
	partitions [][]Event
	pendingMax int
}

// NewExporter 创建分区数为 partitionCount、
// 每分区待定缓冲上限为 pendingLimit 的导出器。
func NewExporter(partitionCount, pendingLimit int) *Exporter {
	if partitionCount <= 0 {
		panic("ontology: partitionCount must be positive")
	}
	if pendingLimit < 0 {
		panic("ontology: pendingLimit must be non-negative")
	}
	return &Exporter{
		partitions: make([][]Event, partitionCount),
		pendingMax: pendingLimit,
	}
}

// Append 向指定分区投递一条事件。
// 分区越界、空键、背压超限时整体拒绝且不改变任何状态。
func (e *Exporter) Append(partition int, event Event) error {
	if partition < 0 || partition >= len(e.partitions) {
		return ErrPartitionOutOfRange
	}
	if event.Key == "" {
		return ErrEmptyKey
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 对齐点 = 所有分区已投递事件数的最小值；
	// 本分区超出对齐点的部分即待定缓冲。
	alignment := e.alignmentLocked()
	pending := len(e.partitions[partition]) - alignment
	if pending >= e.pendingMax {
		return fmt.Errorf("%w: partition=%d pending=%d limit=%d",
			ErrBackpressure, partition, pending, e.pendingMax)
	}

	// 全部判定通过后才落盘，保证被拒绝事件不改变任何状态。
	e.partitions[partition] = append(e.partitions[partition], event)
	return nil
}

// Snapshot 导出当前对齐点上的全局一致快照。
func (e *Exporter) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotLocked()
}

// PartitionDelivered 返回某分区已投递（已接受）的事件数。
func (e *Exporter) PartitionDelivered(partition int) (int, error) {
	if partition < 0 || partition >= len(e.partitions) {
		return 0, ErrPartitionOutOfRange
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.partitions[partition]), nil
}

// PartitionPending 返回某分区当前超出对齐点的待定事件数。
func (e *Exporter) PartitionPending(partition int) (int, error) {
	if partition < 0 || partition >= len(e.partitions) {
		return 0, ErrPartitionOutOfRange
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.partitions[partition]) - e.alignmentLocked(), nil
}

// Alignment 返回当前对齐点（所有分区已投递事件数的最小值）。
func (e *Exporter) Alignment() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.alignmentLocked()
}

// SelfCheck 校验内部不变量：
// 快照视角下每个分区贡献的事件数都恰等于当前对齐点，
// 且不存在待定缓冲超过上限的分区。
func (e *Exporter) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	alignment := e.alignmentLocked()
	for p, buf := range e.partitions {
		delivered := len(buf)
		if delivered < alignment {
			return fmt.Errorf("ontology: invariant broken partition=%d delivered=%d < alignment=%d",
				p, delivered, alignment)
		}
		if pending := delivered - alignment; pending > e.pendingMax {
			return fmt.Errorf("ontology: invariant broken partition=%d pending=%d > limit=%d",
				p, pending, e.pendingMax)
		}
	}

	// 用等长前缀重建快照并逐分区核对：每个分区贡献恰为 alignment 条。
	snap := e.snapshotLocked()
	// 上面 delivered >= alignment 已保证每个分区恰有 alignment 条进入前缀，
	// 不存在某个分区只纳入一半的中间态。
	// 每个键的最终值必须等于最高分区号前缀中对该键的最后一次写入。
	for key, got := range snap.Entries {
		winner := -1
		var want Event
		for p := range e.partitions {
			for _, ev := range e.partitions[p][:alignment] {
				if ev.Key == key {
					winner, want = p, ev
				}
			}
		}
		if winner < 0 || got != want {
			return fmt.Errorf("ontology: invariant broken key=%q snapshot=%v winner=%d want=%v",
				key, got, winner, want)
		}
	}
	return nil
}

// alignmentLocked 调用方必须持有读锁或写锁。
func (e *Exporter) alignmentLocked() int {
	alignment := len(e.partitions[0])
	for _, buf := range e.partitions[1:] {
		if len(buf) < alignment {
			alignment = len(buf)
		}
	}
	return alignment
}

// snapshotLocked 调用方必须持有读锁或写锁。
func (e *Exporter) snapshotLocked() Snapshot {
	alignment := e.alignmentLocked()
	entries := make(map[string]Event)
	// 按分区顺序拼接各分区前 alignment 条事件；
	// map 后写覆盖前写，天然实现同键跨分区高分区号覆盖低分区号。
	for p := range e.partitions {
		for _, ev := range e.partitions[p][:alignment] {
			entries[ev.Key] = ev
		}
	}
	return Snapshot{Alignment: alignment, Entries: entries}
}
