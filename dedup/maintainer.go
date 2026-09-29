// Package dedup 实现多源并集的增量去重维护器：
// 按分区引用计数维护所有分区的去重并集视图。
package dedup

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 三类互不相同的可判定错误。
var (
	// ErrInvalidPartitionCount 分区数非正。
	ErrInvalidPartitionCount = errors.New("dedup: partition count must be positive")
	// ErrPartitionOutOfRange 分区下标越界。
	ErrPartitionOutOfRange = errors.New("dedup: partition index out of range")
	// ErrEmptyElement 元素为空。
	ErrEmptyElement = errors.New("dedup: element must not be empty")
)

// OpType 变更日志条目类型。
type OpType string

const (
	// OpAdd 元素引用计数从零变一，进入视图。
	OpAdd OpType = "+"
	// OpRemove 元素引用计数从一变零，撤出视图。
	OpRemove OpType = "-"
)

// Entry 变更日志条目，按操作顺序追加，下游按序应用。
type Entry struct {
	Seq       int    // 全局单调递增序号，从 0 开始
	Op        OpType // "+" 或 "-"
	Partition int    // 触发该变更的分区
	Element   string // 变更元素
}

// Op 批量操作中的一项。
type Op struct {
	Partition int
	Element   string
}

// Maintainer 多源并集增量去重维护器，并发安全。
type Maintainer struct {
	mu         sync.RWMutex
	partitions []map[string]struct{} // 每个分区持有的元素集合
	refCount   map[string]int        // 元素 -> 持有它的分区个数
	log        []Entry               // 变更日志，按操作顺序追加
}

// NewMaintainer 创建维护器；numPartitions 非正时返回 ErrInvalidPartitionCount。
func NewMaintainer(numPartitions int) (*Maintainer, error) {
	if numPartitions <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidPartitionCount, numPartitions)
	}
	return &Maintainer{
		partitions: make([]map[string]struct{}, numPartitions),
		refCount:   make(map[string]int),
	}, nil
}

// NumPartitions 返回分区数。
func (m *Maintainer) NumPartitions() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.partitions)
}

// validate 校验单条操作的分区与元素。
func (m *Maintainer) validate(partition int, element string) error {
	if partition < 0 || partition >= len(m.partitions) {
		return fmt.Errorf("%w: partition %d not in [0,%d)", ErrPartitionOutOfRange, partition, len(m.partitions))
	}
	if element == "" {
		return ErrEmptyElement
	}
	return nil
}

// Add 将元素加入分区。幂等：已在该分区则无操作。
// 引用计数从零变一时向日志追加一条加条目。
func (m *Maintainer) Add(partition int, element string) error {
	return m.AddBatch([]Op{{Partition: partition, Element: element}})
}

// Remove 将元素从分区撤回。幂等：不在该分区则无操作。
// 引用计数从一变零时向日志追加一条减条目。
func (m *Maintainer) Remove(partition int, element string) error {
	return m.RemoveBatch([]Op{{Partition: partition, Element: element}})
}

// AddBatch 原子批量加入：任一条被拒则整批不生效。
func (m *Maintainer) AddBatch(ops []Op) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, op := range ops {
		if err := m.validate(op.Partition, op.Element); err != nil {
			return err
		}
	}
	for _, op := range ops {
		m.applyAdd(op.Partition, op.Element)
	}
	return nil
}

// RemoveBatch 原子批量撤回：任一条被拒则整批不生效。
func (m *Maintainer) RemoveBatch(ops []Op) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, op := range ops {
		if err := m.validate(op.Partition, op.Element); err != nil {
			return err
		}
	}
	for _, op := range ops {
		m.applyRemove(op.Partition, op.Element)
	}
	return nil
}

// applyAdd 在持锁状态下执行加入。
func (m *Maintainer) applyAdd(partition int, element string) {
	set := m.partitions[partition]
	if set == nil {
		set = make(map[string]struct{})
		m.partitions[partition] = set
	}
	if _, ok := set[element]; ok {
		return // 幂等：已在该分区
	}
	set[element] = struct{}{}
	m.refCount[element]++
	if m.refCount[element] == 1 {
		m.appendLog(OpAdd, partition, element)
	}
}

// applyRemove 在持锁状态下执行撤回。
func (m *Maintainer) applyRemove(partition int, element string) {
	set := m.partitions[partition]
	if set == nil {
		return // 幂等：分区为空
	}
	if _, ok := set[element]; !ok {
		return // 幂等：不在该分区
	}
	delete(set, element)
	m.refCount[element]--
	if m.refCount[element] == 0 {
		delete(m.refCount, element)
		m.appendLog(OpRemove, partition, element)
	}
}

func (m *Maintainer) appendLog(op OpType, partition int, element string) {
	m.log = append(m.log, Entry{
		Seq:       len(m.log),
		Op:        op,
		Partition: partition,
		Element:   element,
	})
}

// View 返回当前去重并集视图（引用计数 >= 1 的元素集合），排序后返回副本。
func (m *Maintainer) View() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	view := make([]string, 0, len(m.refCount))
	for e := range m.refCount {
		view = append(view, e)
	}
	sort.Strings(view)
	return view
}

// Log 返回变更日志副本。
func (m *Maintainer) Log() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Entry, len(m.log))
	copy(out, m.log)
	return out
}

// RefCount 返回元素当前引用计数（并发只读自检用）。
func (m *Maintainer) RefCount(element string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.refCount[element]
}

// SelfCheck 校验内部一致性：引用计数与分区集合完全吻合，即与批量重算一致。
func (m *Maintainer) SelfCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	want := make(map[string]int)
	for _, set := range m.partitions {
		for e := range set {
			want[e]++
		}
	}
	if len(want) != len(m.refCount) {
		return fmt.Errorf("dedup: self-check failed: refCount has %d elements, recomputed %d", len(m.refCount), len(want))
	}
	for e, c := range want {
		if m.refCount[e] != c {
			return fmt.Errorf("dedup: self-check failed: element %q refCount=%d, recomputed %d", e, m.refCount[e], c)
		}
	}
	return nil
}
