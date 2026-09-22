// Package snapshot 维护读快照与活跃写事务集合。
//
// 快照点 = 建立那一刻事务号源 Current()+1（不消耗事务号），
// 活跃集合 = 建立那一刻仍未提交的写事务号集合。
// 版本 v 对快照可见 ⟺ v.Commit < Point 且 v.Commit ∉ 活跃集合。
package snapshot

import (
	"errors"
	"math"
	"sync"

	"ontology/txid"
)

// ErrLimit 表示活跃快照数达到上限。
var ErrLimit = errors.New("snapshot: 活跃快照数超限")

// ErrExhausted 表示快照点无法表示（事务号空间顶格）。
var ErrExhausted = errors.New("snapshot: 事务号空间顶格，无法建立快照点")

// Snapshot 是一个读快照，建立后不可变，实现 version.View。
type Snapshot struct {
	point  txid.ID
	active map[txid.ID]struct{}
}

// Point 返回快照点（可见性右边界，取不到）。
func (s *Snapshot) Point() txid.ID { return s.point }

// IsActive 报告提交事务号 id 在快照建立时是否仍未提交。
func (s *Snapshot) IsActive(id txid.ID) bool {
	_, ok := s.active[id]
	return ok
}

// horizon 返回该快照的回收视野：任何被提交事务号 < horizon 的版本
// 遮蔽的旧版本，对本快照一定不可见。
func (s *Snapshot) horizon() txid.ID {
	h := s.point
	for id := range s.active {
		if id.Before(h) {
			h = id
		}
	}
	return h
}

// Manager 管理活跃快照与活跃写事务，并发安全。
type Manager struct {
	mu      sync.Mutex
	src     txid.Source
	maxOpen int
	open    map[*Snapshot]struct{}
	writers map[txid.ID]struct{}
}

// NewManager 创建管理器。maxOpen <= 0 表示不限制活跃快照数。
func NewManager(src txid.Source, maxOpen int) *Manager {
	return &Manager{
		src:     src,
		maxOpen: maxOpen,
		open:    map[*Snapshot]struct{}{},
		writers: map[txid.ID]struct{}{},
	}
}

// Begin 建立一个读快照。快照点与活跃集合在同一把锁内确定，
// 与 BeginWriter / Horizon 互斥，保证不出现竞态。
func (m *Manager) Begin() (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maxOpen > 0 && len(m.open) >= m.maxOpen {
		return nil, ErrLimit
	}
	cur := m.src.Current()
	if cur == txid.ID(math.MaxUint64) {
		return nil, ErrExhausted
	}
	s := &Snapshot{
		point:  cur + 1,
		active: make(map[txid.ID]struct{}, len(m.writers)),
	}
	for id := range m.writers {
		s.active[id] = struct{}{}
	}
	m.open[s] = struct{}{}
	return s, nil
}

// Release 关闭一个快照。
func (m *Manager) Release(s *Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.open, s)
}

// BeginWriter 分配一个写事务号并登记为活跃写事务。
// 分配与登记在同一把锁内完成，保证新快照要么看不到该事务号
// （point <= id），要么把它收入活跃集合，不存在中间态。
func (m *Manager) BeginWriter() (txid.ID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := m.src.Next()
	if err != nil {
		return txid.Invalid, err
	}
	m.writers[id] = struct{}{}
	return id, nil
}

// EndWriter 注销一个活跃写事务（提交或回滚后调用）。
func (m *Manager) EndWriter(id txid.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.writers, id)
}

// Horizon 返回当前回收视野：所有活跃快照 horizon 的最小值。
// 没有活跃快照时第二个返回值为 false（视野无界）。
func (m *Manager) Horizon() (txid.ID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.open) == 0 {
		return txid.Invalid, false
	}
	var h txid.ID
	first := true
	for s := range m.open {
		sh := s.horizon()
		if first || sh.Before(h) {
			h = sh
			first = false
		}
	}
	return h, true
}

// Count 返回活跃快照数。
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.open)
}
