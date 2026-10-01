// Package walreclaim 管理多列族预写日志（WAL）的回收水位。
//
// 它跟踪每个列族的活跃内存表与冻结队列对日志文件的依赖，
// 使可回收日志编号集合始终是“仍无任何未落盘数据依赖”的前缀。
package walreclaim

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因，按调用上的校验顺序排列。
var (
	ErrEmptyName    = errors.New("walreclaim: column family name is empty")
	ErrExists       = errors.New("walreclaim: column family already exists")
	ErrNotFound     = errors.New("walreclaim: column family not found")
	ErrEmptyActive  = errors.New("walreclaim: active memtable is empty")
	ErrEmptyFlushed = errors.New("walreclaim: frozen memtable queue is empty")
)

// memtable 是一个（活跃或冻结的）内存表。
// first 为它首次被写入时的当前日志编号；活跃表为空时 first == 0。
type memtable struct {
	first int64
}

// cf 是一个列族：一个可空的活跃内存表加按冻结先后排列的队列。
type cf struct {
	active *memtable // nil 表示活跃表为空
	frozen []*memtable
}

// Manager 是并发安全的日志回收水位管理器。
// 零值不可用，必须通过 NewManager 创建。
type Manager struct {
	mu sync.Mutex

	cur    int64          // 当前日志编号，从 1 起；Roll 后加 1
	cfs    map[string]*cf // 现存列族（DropCF 后键被删除，名字可重建）
	firsts map[int64]int  // 全部未落盘内存表 first 值的多重集合
	purged int64          // 已回收前缀的高水位：编号 1..purged 均已 Purge
}

// NewManager 创建管理器：日志编号从 1 起。
func NewManager() *Manager {
	return &Manager{
		cur:    1,
		cfs:    make(map[string]*cf),
		firsts: make(map[int64]int),
	}
}

// minFirst 返回全部未落盘内存表 first 的最小值；没有未落盘表时返回 0。
// 调用方必须持有 m.mu。
func (m *Manager) minFirst() int64 {
	var min int64
	for f := range m.firsts {
		if min == 0 || f < min {
			min = f
		}
	}
	return min
}

// obsoleteLocked 计算当前可回收编号的升序序列。调用方必须持有 m.mu。
//
// 回收条件（同时满足）：
//   - w < cur（当前日志永不可回收）；
//   - w > purged（尚未被 Purge，已回收集合恒为前缀）；
//   - 不存在未落盘表，或 w 严格小于全部未落盘表 first 的最小值
//     （w 恰等于某表 first 时不可回收）。
func (m *Manager) obsoleteLocked() []int64 {
	high := m.cur - 1
	if min := m.minFirst(); min != 0 && min-1 < high {
		high = min - 1
	}
	if high <= m.purged {
		return nil
	}
	out := make([]int64, 0, high-m.purged)
	for w := m.purged + 1; w <= high; w++ {
		out = append(out, w)
	}
	return out
}

// CreateCF 创建一个全新的列族（活跃表为空）。
func (m *Manager) CreateCF(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cfs[name]; ok {
		return ErrExists
	}
	m.cfs[name] = &cf{}
	return nil
}

// Write 向指定列族的活跃内存表写入；
// 活跃表为空时它在本次写入时取当时的 cur 作为 first。
func (m *Manager) Write(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cfs[name]
	if !ok {
		return ErrNotFound
	}
	if c.active == nil {
		c.active = &memtable{first: m.cur}
		m.firsts[m.cur]++
	}
	return nil
}

// Roll 将当前日志编号加 1，返回滚动后的编号。
func (m *Manager) Roll() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cur++
	return m.cur
}

// FlushStart 把非空活跃表冻结到队尾，然后活跃表变空。
func (m *Manager) FlushStart(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cfs[name]
	if !ok {
		return ErrNotFound
	}
	if c.active == nil {
		return ErrEmptyActive
	}
	c.frozen = append(c.frozen, c.active)
	c.active = nil
	return nil
}

// FlushDone 移除该列族队首（最旧）的冻结内存表。
func (m *Manager) FlushDone(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cfs[name]
	if !ok {
		return ErrNotFound
	}
	if len(c.frozen) == 0 {
		return ErrEmptyFlushed
	}
	old := c.frozen[0]
	c.frozen = c.frozen[1:]
	m.removeFirst(old.first)
	return nil
}

// DropCF 使该列族全部内存表作废，列族名随后可重新创建。
func (m *Manager) DropCF(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cfs[name]
	if !ok {
		return ErrNotFound
	}
	if c.active != nil {
		m.removeFirst(c.active.first)
	}
	for _, t := range c.frozen {
		m.removeFirst(t.first)
	}
	delete(m.cfs, name)
	return nil
}

// Obsolete 返回当前可回收日志编号的升序序列，不改变任何状态。
func (m *Manager) Obsolete() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.obsoleteLocked()
}

// Purge 把当前可回收编号标记为已回收并返回它们（升序）。
func (m *Manager) Purge() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.obsoleteLocked()
	if len(out) > 0 {
		m.purged = out[len(out)-1]
	}
	return out
}

// removeFirst 从 first 多重集合中移除一个值。调用方必须持有 m.mu。
func (m *Manager) removeFirst(f int64) {
	switch m.firsts[f] {
	case 0:
		// 内部一致性不应出现；防御性忽略。
	case 1:
		delete(m.firsts, f)
	default:
		m.firsts[f]--
	}
}
