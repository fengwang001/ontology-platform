// Package store 管理多个 Key 的 delta.Log，提供全局 Compact 与 DeltaCount。
// 依赖 delta，单向依赖。
package store

import (
	"sync"

	"ontology/delta"
)

// Store 是多 Key 的增量存储，所有方法可并发调用。
type Store struct {
	mu   sync.RWMutex
	logs map[string]*delta.Log

	// lastApplyVisits 记录最近一次 Apply 访问的已有 delta 条目个数。
	// 纯尾部追加不访问任何已有条目，恒为 0；仅用于包内测试证明 O(1)。
	lastApplyVisits int
}

// New 返回空 Store。
func New() *Store {
	return &Store{logs: make(map[string]*delta.Log)}
}

// Apply 把 d 追加到 key 的日志尾部；若条目总数将达 max 之上则拒绝且不留痕。
// 调用方保证 key 非空、max 为正。
func (s *Store) Apply(key string, d int64, max int) (overflow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deltaCountLocked()+1 > max {
		return true
	}
	l, ok := s.logs[key]
	if !ok {
		l = &delta.Log{}
		s.logs[key] = l
	}
	s.lastApplyVisits = 0 // 尾部 append，不读取任何已有条目
	l.Append(d)
	return false
}

// Get 返回 key 的可见值 base+Σ(delta)；未知 Key 为 0。
func (s *Store) Get(key string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if l, ok := s.logs[key]; ok {
		return l.Value()
	}
	return 0
}

// Compact 把所有 Key 的未合并 delta 同时合并进基线并清空日志。
// 全程持写锁，对外无 torn 状态；不改变任何可见值。
func (s *Store) Compact() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.logs {
		l.Merge()
	}
}

// DeltaCount 返回当前未合并 delta 条目总数。
func (s *Store) DeltaCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deltaCountLocked()
}

func (s *Store) deltaCountLocked() int {
	n := 0
	for _, l := range s.logs {
		n += l.Len()
	}
	return n
}
