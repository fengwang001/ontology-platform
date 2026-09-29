// Package dedup 提供多源并集的增量去重维护器。
//
// 每个分区（数据源）持有一个元素集合，维护器通过全局引用计数维护
// 所有分区的去重并集视图：元素仅在没有任何分区再持有它时才从视图撤下，
// 且增量维护的结果与对所有分区做批量重算的结果一致。
package dedup

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 三类可判定的非法输入错误，彼此互不相同，可用 errors.Is 判定。
var (
	// ErrInvalidPartitionCount 表示构造维护器时分区数非正。
	ErrInvalidPartitionCount = errors.New("dedup: partition count must be positive")
	// ErrPartitionOutOfRange 表示操作的分区下标越界。
	ErrPartitionOutOfRange = errors.New("dedup: partition out of range")
	// ErrEmptyElement 表示元素为空。
	ErrEmptyElement = errors.New("dedup: element must not be empty")
)

// ChangeKind 是变更日志条目的类型。
type ChangeKind int

const (
	// KindAdd 表示元素引用计数由零变一，进入去重并集视图。
	KindAdd ChangeKind = iota + 1
	// KindRemove 表示元素引用计数由一变零，从去重并集视图撤下。
	KindRemove
)

// Change 是一条变更日志，按操作顺序追加；下游按序应用即可复现视图。
type Change struct {
	Kind     ChangeKind
	Element  string
	RefCount int
}

// Op 表示一次分区级元素操作。
type Op struct {
	Partition int
	Element   string
}

// batchKey 标识批内一条“分区持有元素”的关系；同一关系在一批内只生效一次。
type batchKey struct {
	partition int
	element   string
}

// Maintainer 是多源并集的增量去重维护器。
//
// 所有方法均并发安全：Add/Remove 等写操作互斥串行化，
// View/Log/Has/RefCount/SelfCheck 等读操作与写操作互斥，
// 因此并发只读同一实例得到的视图必然逐字段相同。
type Maintainer struct {
	mu       sync.RWMutex
	n        int
	parts    []map[string]struct{}
	refCount map[string]int
	log      []Change
}

// New 创建一个持有 n 个分区的维护器；n 非正时返回 ErrInvalidPartitionCount。
func New(n int) (*Maintainer, error) {
	if n <= 0 {
		return nil, ErrInvalidPartitionCount
	}
	parts := make([]map[string]struct{}, n)
	for i := range parts {
		parts[i] = make(map[string]struct{})
	}
	return &Maintainer{
		n:        n,
		parts:    parts,
		refCount: make(map[string]int),
	}, nil
}

// Add 把元素加入指定分区（按分区幂等）。
// 分区越界返回 ErrPartitionOutOfRange；元素为空返回 ErrEmptyElement。
// 元素已在该分区时无操作；引用计数从零变一时追加一条 KindAdd 日志，否则不输出。
func (m *Maintainer) Add(partition int, element string) error {
	if err := validate(m.n, partition, element); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addLocked(partition, element)
	return nil
}

// Remove 把元素从指定分区撤回（按分区幂等）。
// 分区越界返回 ErrPartitionOutOfRange；元素为空返回 ErrEmptyElement。
// 元素不在该分区时无操作；引用计数从一变零时追加一条 KindRemove 日志，否则不输出。
func (m *Maintainer) Remove(partition int, element string) error {
	if err := validate(m.n, partition, element); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(partition, element)
	return nil
}

// AddBatch 原子地批量加入；任一条非法则整批不生效。
// 重复元素在同一批内只生效一次；成功后按切片顺序处理并追加日志。
func (m *Maintainer) AddBatch(ops []Op) error {
	if err := validateBatch(m.n, ops); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[batchKey]struct{}, len(ops))
	for _, op := range ops {
		key := batchKey{partition: op.Partition, element: op.Element}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		m.addLocked(op.Partition, op.Element)
	}
	return nil
}

// RemoveBatch 原子地批量撤回；任一条非法则整批不生效。
// 重复元素在同一批内只生效一次；成功后按切片顺序处理并追加日志。
func (m *Maintainer) RemoveBatch(ops []Op) error {
	if err := validateBatch(m.n, ops); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[batchKey]struct{}, len(ops))
	for _, op := range ops {
		key := batchKey{partition: op.Partition, element: op.Element}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		m.removeLocked(op.Partition, op.Element)
	}
	return nil
}

// View 返回当前去重并集视图（引用计数不小于一的元素集合）的快照副本。
// 返回的 map 可由调用方自由修改，不影响维护器内部状态。
func (m *Maintainer) View() map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	view := make(map[string]struct{}, len(m.refCount))
	for element, count := range m.refCount {
		if count >= 1 {
			view[element] = struct{}{}
		}
	}
	return view
}

// Has 报告元素当前是否在视图中。
func (m *Maintainer) Has(element string) bool {
	if element == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.refCount[element] >= 1
}

// RefCount 返回元素当前的全局引用计数。
func (m *Maintainer) RefCount(element string) int {
	if element == "" {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.refCount[element]
}

// PartitionCount 返回分区数。
func (m *Maintainer) PartitionCount() int {
	return m.n
}

// Log 返回变更日志的快照副本（按追加顺序排列）。
// 空批次或无效果的幂等操作不产生日志。
func (m *Maintainer) Log() []Change {
	m.mu.RLock()
	defer m.mu.RUnlock()
	log := make([]Change, len(m.log))
	copy(log, m.log)
	return log
}

// SelfCheck 对照“持有该元素的分区个数”重算引用计数，
// 并校验视图与日志不变量；一致时返回 nil。
func (m *Maintainer) SelfCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	expected := make(map[string]int)
	for _, part := range m.parts {
		if part == nil {
			return errors.New("dedup: internal error: nil partition set")
		}
		for element := range part {
			expected[element]++
		}
	}

	if len(expected) != len(m.refCount) {
		return fmt.Errorf("dedup: refcount key mismatch: recomputed %d elements, tracked %d",
			len(expected), len(m.refCount))
	}
	for element, want := range expected {
		got := m.refCount[element]
		if got != want {
			return fmt.Errorf("dedup: refcount mismatch for %q: recomputed %d, tracked %d",
				element, want, got)
		}
		if got <= 0 {
			return fmt.Errorf("dedup: non-positive refcount %d for %q", got, element)
		}
	}

	seen := make(map[string]bool, len(m.log))
	for i, change := range m.log {
		switch change.Kind {
		case KindAdd:
			if seen[change.Element] {
				return fmt.Errorf("dedup: log index %d: duplicate add for %q without remove", i, change.Element)
			}
			seen[change.Element] = true
			if change.RefCount != 1 {
				return fmt.Errorf("dedup: log index %d: add refcount = %d, want 1", i, change.RefCount)
			}
		case KindRemove:
			if !seen[change.Element] {
				return fmt.Errorf("dedup: log index %d: remove for %q without prior add", i, change.Element)
			}
			seen[change.Element] = false
			if change.RefCount != 0 {
				return fmt.Errorf("dedup: log index %d: remove refcount = %d, want 0", i, change.RefCount)
			}
		default:
			return fmt.Errorf("dedup: log index %d: unknown change kind %d", i, change.Kind)
		}
	}

	inView := make([]string, 0)
	for element, present := range seen {
		if present {
			inView = append(inView, element)
		}
	}
	sort.Strings(inView)
	for _, element := range inView {
		if _, ok := expected[element]; !ok {
			return fmt.Errorf("dedup: log implies %q in view but refcount says absent", element)
		}
	}
	return nil
}

// validate 校验单条操作的分区下标与元素，三类错误互不相同、可判定。
func validate(n, partition int, element string) error {
	if partition < 0 || partition >= n {
		return ErrPartitionOutOfRange
	}
	if element == "" {
		return ErrEmptyElement
	}
	return nil
}

// validateBatch 在不触碰内部状态的前提下预检整批，保证非法批次整批不生效。
func validateBatch(n int, ops []Op) error {
	for _, op := range ops {
		if err := validate(n, op.Partition, op.Element); err != nil {
			return err
		}
	}
	return nil
}

// addLocked 执行加入；调用方必须持有写锁。
func (m *Maintainer) addLocked(partition int, element string) {
	part := m.parts[partition]
	if _, ok := part[element]; ok {
		return
	}
	part[element] = struct{}{}
	count := m.refCount[element] + 1
	m.refCount[element] = count
	if count == 1 {
		m.log = append(m.log, Change{Kind: KindAdd, Element: element, RefCount: 1})
	}
}

// removeLocked 执行撤回；调用方必须持有写锁。
func (m *Maintainer) removeLocked(partition int, element string) {
	part := m.parts[partition]
	if _, ok := part[element]; !ok {
		return
	}
	delete(part, element)
	count := m.refCount[element] - 1
	if count == 0 {
		delete(m.refCount, element)
		m.log = append(m.log, Change{Kind: KindRemove, Element: element, RefCount: 0})
		return
	}
	m.refCount[element] = count
}
