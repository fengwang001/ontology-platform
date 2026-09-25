// Package snapshot 在版本化 store 之上实现写时复制的只读快照。
package snapshot

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/store"
)

// ErrClosed 表示快照已关闭，不能再读取或续传。
var ErrClosed = errors.New("snapshot: snapshot closed")

// Snapshot 是固定版本水位的只读句柄。保留值只挂在当前最老快照上。
type Snapshot struct {
	id     int64
	ver    int64
	closed bool
	retain map[string]store.Cell
	reads  atomic.Int64
	readWG sync.WaitGroup
}

// Manager 协调写入与活跃快照集合。
type Manager struct {
	mu     sync.Mutex
	st     *store.Store
	active []*Snapshot
	nextID int64
}

// NewManager 创建协调器。
func NewManager(st *store.Store) *Manager {
	return &Manager{st: st, nextID: 1}
}

// Open 固定当前版本水位，返回只读快照句柄。
func (m *Manager) Open() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := &Snapshot{
		id:     m.nextID,
		ver:    m.st.Version() - 1,
		retain: make(map[string]store.Cell),
	}
	m.nextID++
	m.active = append(m.active, snap)
	return snap
}

// Put 写入键。若最老活跃快照比本次写入旧，则在覆盖前把快照可见旧值
// （可能是“不存在”）保留到最老快照，且只保留首次覆盖时的旧值。
func (m *Manager) Put(key string, value []byte) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.active) > 0 {
		oldest := m.active[0]
		if _, kept := oldest.retain[key]; !kept {
			oldest.retain[key] = m.st.ReadAt(key, oldest.ver)
		}
	}
	return m.st.Put(key, value)
}

func (m *Manager) beginRead(s *Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.readWG.Add(1)
	return nil
}

func (m *Manager) endRead(s *Snapshot) { s.readWG.Done() }

// Acquire 获取一个覆盖整段导出/续传的读引用；快照已关闭时返回 ErrClosed。
// 必须配对调用 Release。
func (m *Manager) Acquire(s *Snapshot) error { return m.beginRead(s) }

// Release 释放读引用。
func (m *Manager) Release(s *Snapshot) { m.endRead(s) }

// Read 读取快照水位下某键的值，每次成功调用计数一次。
func (m *Manager) Read(s *Snapshot, key string) (store.Cell, error) {
	if err := m.beginRead(s); err != nil {
		return store.Cell{}, err
	}
	defer m.endRead(s)
	m.mu.Lock()
	if c, ok := s.retain[key]; ok {
		m.mu.Unlock()
		s.reads.Add(1)
		return c, nil
	}
	if len(m.active) > 0 && m.active[0] != s {
		if c, ok := m.active[0].retain[key]; ok {
			m.mu.Unlock()
			s.reads.Add(1)
			return c, nil
		}
	}
	ver := s.ver
	m.mu.Unlock()
	c := m.st.ReadAt(key, ver)
	s.reads.Add(1)
	return c, nil
}

// Keys 返回快照可见键，order 为 1 字典序、-1 逆序。
func (m *Manager) Keys(s *Snapshot, order int) ([]string, error) {
	if err := m.beginRead(s); err != nil {
		return nil, err
	}
	defer m.endRead(s)
	m.mu.Lock()
	ver := s.ver
	m.mu.Unlock()
	return m.st.KeysAt(ver, order), nil
}

// Close 等待在途读取结束后关闭快照，并把仍被新最老快照需要的保留值移交。
func (m *Manager) Close(s *Snapshot) {
	m.mu.Lock()
	if s.closed {
		m.mu.Unlock()
		return
	}
	s.closed = true
	idx := -1
	for i, a := range m.active {
		if a == s {
			idx = i
			break
		}
	}
	m.active = append(m.active[:idx], m.active[idx+1:]...)
	var next *Snapshot
	if len(m.active) > 0 {
		next = m.active[0]
	}
	keep := s.retain
	m.mu.Unlock()

	s.readWG.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	if next == nil {
		s.retain = map[string]store.Cell{}
		return
	}
	handed := make(map[string]store.Cell, len(keep))
	for k, c := range keep {
		r := m.st.Current(k)
		if r.Ver > next.ver {
			handed[k] = c
		}
	}
	s.retain = map[string]store.Cell{}
	for k, c := range handed {
		next.retain[k] = c
	}
}

// Version 返回快照水位。
func (s *Snapshot) Version() int64 { return s.ver }

// Retained 返回该快照名下保留值个数。
func (s *Snapshot) Retained() int {
	return len(s.retain)
}

// Reads 返回该快照累计读取次数。
func (s *Snapshot) Reads() int64 { return s.reads.Load() }

// IsActive 报告快照是否仍打开。
func (m *Manager) IsActive(s *Snapshot) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !s.closed
}
