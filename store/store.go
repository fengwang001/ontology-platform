// Package store 是支持单调版本与并发读写的键值存储。
package store

import "sync"

// Retainer 由快照实现：Put 生效前，store 在自身锁内回调它保留旧值。
type Retainer interface {
	SnapVersion() uint64
	Retain(key string, oldValue []byte)
	Release(dec func())
}

type item struct {
	value   []byte
	version uint64
}

// Store 保存每个键的当前值及其写入版本。
type Store struct {
	mu        sync.RWMutex
	data      map[string]item
	version   uint64
	snaps     map[Retainer]struct{}
	retained  int // 全部活跃快照保留值总数（非导出计数器）
	inflight  int
	exporting bool
	done      chan struct{}
}

// New 创建空存储。
func New() *Store {
	return &Store{data: make(map[string]item), snaps: make(map[Retainer]struct{})}
}

// Version 返回当前版本水位。
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Keys 返回当前全部键（顺序由调用方决定，这里无序）。
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// Put 写入新值并推进版本；写前让相关快照保留旧值。
func (s *Store) Put(key string, value []byte) uint64 {
	cp := append([]byte(nil), value...)
	s.mu.Lock()
	defer s.mu.Unlock()
	old, existed := s.data[key]
	s.version++
	ver := s.version
	for r := range s.snaps {
		if r.SnapVersion() < ver && existed {
			r.Retain(key, old.value)
		}
	}
	s.data[key] = item{value: cp, version: ver}
	return ver
}

// Current 返回键的当前值；未存在时 ok=false（与空值可区分）。
func (s *Store) Current(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.data[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), it.value...), true
}

// Register 注册快照，返回当前版本、键集合。
func (s *Store) Register(r Retainer) (uint64, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snaps[r] = struct{}{}
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return s.version, keys
}

// Unregister 注销快照。
func (s *Store) Unregister(r Retainer) {
	s.mu.Lock()
	delete(s.snaps, r)
	s.mu.Unlock()
}

// RetainedCount 返回所有活跃快照保留值的总数。
func (s *Store) RetainedCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retained
}

// AddRetained 调整保留值计数（由快照在 store 锁内调用）。
func (s *Store) AddRetained(delta int) { s.retained += delta }

// BeginExport / EndExport 跟踪进行中的导出，供快照关闭等待。
func (s *Store) BeginExport() {
	s.mu.Lock()
	s.inflight++
	s.mu.Unlock()
}

// EndExport 结束一次导出。
func (s *Store) EndExport() {
	s.mu.Lock()
	s.inflight--
	if s.exporting && s.inflight == 0 {
		close(s.done)
		s.exporting = false
		s.done = nil
	}
	s.mu.Unlock()
}

// WaitExports 阻塞到当前所有在途导出结束。
func (s *Store) WaitExports() {
	s.mu.Lock()
	if s.inflight == 0 {
		s.mu.Unlock()
		return
	}
	if !s.exporting {
		s.exporting = true
		s.done = make(chan struct{})
	}
	ch := s.done
	s.mu.Unlock()
	<-ch
}
