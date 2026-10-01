// Package wal implements a multi-column-family WAL recycle watermark manager.
//
// 管理器跟踪各列族未落盘内存表（活跃表与冻结队列）对日志文件的依赖，
// 使可回收的日志编号集合始终恰好等于仍无任何未落盘数据依赖的前缀。
package wal

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因。同一调用上按下列顺序只报第一个：
// 列族名为空、重名创建、列族不存在、FlushStart 时活跃表为空、FlushDone 时冻结队列为空。
var (
	ErrEmptyCFName         = errors.New("wal: column family name is empty")
	ErrCFAlreadyExists     = errors.New("wal: column family already exists")
	ErrCFNotFound          = errors.New("wal: column family not found")
	ErrActiveMemtableEmpty = errors.New("wal: active memtable is empty")
	ErrNoFrozenMemtable    = errors.New("wal: no frozen memtable")
)

// memtable 是一个内存表。first 为它首次被写入时的当前日志编号；
// 空的活跃表没有 first（hasFirst 为 false）。
type memtable struct {
	first    uint64
	hasFirst bool
}

// columnFamily 持有一个活跃内存表和按冻结先后排列的冻结队列。
type columnFamily struct {
	active memtable
	frozen []memtable
}

// Manager 是 WAL 回收水位管理器，所有方法可并发调用，
// 结果等价于某个串行顺序。
type Manager struct {
	mu sync.Mutex

	cur        uint64 // 当前日志编号，初始为 1
	purgedUpTo uint64 // 已回收编号恒为前缀 (0, purgedUpTo]

	cfs    map[string]*columnFamily
	firsts *firstSet // 全部未落盘表 first 的多重集合（增量维护最小值）
}

// NewManager 创建一个管理器，当前日志编号 cur 初始为 1。
func NewManager() *Manager {
	return &Manager{cur: 1, cfs: make(map[string]*columnFamily), firsts: newFirstSet()}
}

// CreateCF 创建列族。名为空或重名（含未 Drop 的）时整体拒绝。
func (m *Manager) CreateCF(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return ErrEmptyCFName
	}
	if _, ok := m.cfs[name]; ok {
		return ErrCFAlreadyExists
	}
	m.cfs[name] = &columnFamily{}
	return nil
}

// DropCF 作废列族全部内存表，不再计入回收判断；之后该名可重新创建。
func (m *Manager) DropCF(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return ErrEmptyCFName
	}
	cf, ok := m.cfs[name]
	if !ok {
		return ErrCFNotFound
	}
	if cf.active.hasFirst {
		m.firsts.remove(cf.active.first)
	}
	for _, mt := range cf.frozen {
		m.firsts.remove(mt.first)
	}
	delete(m.cfs, name)
	return nil
}

// Roll 使当前日志编号加 1。
func (m *Manager) Roll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cur++
}

// Write 向列族活跃表写入；空活跃表在首次写入时取当时的 cur 作为 first。
func (m *Manager) Write(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return ErrEmptyCFName
	}
	cf, ok := m.cfs[name]
	if !ok {
		return ErrCFNotFound
	}
	if !cf.active.hasFirst {
		cf.active.first = m.cur
		cf.active.hasFirst = true
		m.firsts.add(m.cur)
	}
	return nil
}

// FlushStart 把非空活跃表冻结入队尾，活跃表变空。
func (m *Manager) FlushStart(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return ErrEmptyCFName
	}
	cf, ok := m.cfs[name]
	if !ok {
		return ErrCFNotFound
	}
	if !cf.active.hasFirst {
		return ErrActiveMemtableEmpty
	}
	cf.frozen = append(cf.frozen, cf.active)
	cf.active = memtable{}
	return nil
}

// FlushDone 移除该列族队首（最旧）冻结表。
func (m *Manager) FlushDone(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return ErrEmptyCFName
	}
	cf, ok := m.cfs[name]
	if !ok {
		return ErrCFNotFound
	}
	if len(cf.frozen) == 0 {
		return ErrNoFrozenMemtable
	}
	m.firsts.remove(cf.frozen[0].first)
	cf.frozen = cf.frozen[1:]
	return nil
}

// recyclableLocked 计算当前可回收编号上界 hi：
// 可回收集合恰好是 (purgedUpTo, hi] 内的全部整数。
// 日志 w 可回收当且仅当 w < cur、w 未被 Purge、
// 且 w 严格小于全部未落盘表 first 的最小值（无未落盘表则不受此限）。
func (m *Manager) recyclableLocked() (hi uint64) {
	hi = m.cur - 1 // w < cur，当前日志永不可回收
	if min, ok := m.firsts.min(); ok && min-1 < hi {
		hi = min - 1 // w 严格小于最小 first，w == first 不可回收
	}
	return hi
}

// obsoleteLocked 返回 (purgedUpTo, hi] 的升序编号。
func (m *Manager) obsoleteLocked() []uint64 {
	hi := m.recyclableLocked()
	if hi <= m.purgedUpTo {
		return nil
	}
	out := make([]uint64, 0, hi-m.purgedUpTo)
	for w := m.purgedUpTo + 1; w <= hi; w++ {
		out = append(out, w)
	}
	return out
}

// Obsolete 返回当前可回收日志编号（升序），不改变状态。
func (m *Manager) Obsolete() []uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.obsoleteLocked()
}

// Purge 把当前可回收编号全部标为已回收并返回（升序）。
func (m *Manager) Purge() []uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.obsoleteLocked()
	if len(out) > 0 {
		m.purgedUpTo = out[len(out)-1]
	}
	return out
}
