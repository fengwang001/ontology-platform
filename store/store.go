// Package store 是支持并发写入与按版本读取的键值存储。
package store

import "sync"

// Entry 是键在某一时刻的可见状态。
// Absent 为 true 表示该键不存在，用以区别「不存在」与「值为空字节串」。
type Entry struct {
	Value  []byte
	Ver    uint64
	Absent bool
}

// Preserver 由快照实现：写入覆盖旧值前，store 通知它保留旧 entry。
type Preserver interface {
	Watermark() uint64
	Preserve(key string, old Entry)
}

// Store 是并发安全的版本化键值存储。
type Store struct {
	mu       sync.RWMutex
	data     map[string]Entry
	clock    uint64
	snapsMu  sync.RWMutex
	snaps    map[Preserver]struct{}
}

// New 创建空存储。
func New() *Store {
	return &Store{
		data:  make(map[string]Entry),
		snaps: make(map[Preserver]struct{}),
	}
}

// Register 登记一个快照，使后续写入向它保留旧值。
func (s *Store) Register(p Preserver) {
	s.snapsMu.Lock()
	s.snaps[p] = struct{}{}
	s.snapsMu.Unlock()
}

// Version 返回当前提交版本（新快照的水位取此值）。
func (s *Store) Version() uint64 {
	s.mu.RLock()
	v := s.clock
	s.mu.RUnlock()
	return v
}

// Unregister 移除快照；返回后不再有新的保留回调指向它。
func (s *Store) Unregister(p Preserver) {
	s.snapsMu.Lock()
	delete(s.snaps, p)
	s.snapsMu.Unlock()
}

// Put 写入键值并分配新版本；覆盖前通知所有水位更旧的快照保留旧 entry。
func (s *Store) Put(key, value string) uint64 {
	s.mu.Lock()
	s.clock++
	ver := s.clock
	old := s.data[key]
	if old.Ver == 0 {
		old = Entry{Absent: true}
	}
	next := Entry{Value: []byte(value), Ver: ver}
	s.data[key] = next

	s.snapsMu.RLock()
	for p := range s.snaps {
		if p.Watermark() < ver {
			p.Preserve(key, old)
		}
	}
	s.snapsMu.RUnlock()
	s.mu.Unlock()
	return ver
}

// Current 返回键的当前 entry（不存在时 Absent 为 true）。
func (s *Store) Current(key string) Entry {
	s.mu.RLock()
	e := s.data[key]
	s.mu.RUnlock()
	if e.Ver == 0 {
		e.Absent = true
	}
	return e
}

// Keys 返回当前全部键的快照切片（调用方排序后使用）。
func (s *Store) Keys() []string {
	s.mu.RLock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	return keys
}

// Len 返回当前键数。
func (s *Store) Len() int {
	s.mu.RLock()
	n := len(s.data)
	s.mu.RUnlock()
	return n
}
