// Package store 管理多 Key 的 delta.Entry，提供全局 Compact 与 DeltaCount。
// 依赖 delta；并发安全由内部互斥锁保证。
package store

import (
	"errors"
	"sync"

	"ontology/delta"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmptyKey = errors.New("store: key must not be empty")
	ErrCapacity = errors.New("store: delta capacity exceeded")
	ErrBadMax   = errors.New("store: maxDeltas must be positive")
)

// Store 是多 Key 的增量存储。max 为 delta 日志条目总数上限。
type Store struct {
	mu      sync.RWMutex
	entries map[string]*delta.Entry
	max     int
	count   int // 当前 delta 日志条目总数
	// lastVisited 记录最近一次 Apply 访问的已有 delta 条目个数。
	// 非导出，仅同包测试可直接读取，不出现在任何公开接口。
	lastVisited int
}

// New 构造容量为 max 的 Store；max 必须为正。
func New(max int) (*Store, error) {
	if max <= 0 {
		return nil, ErrBadMax
	}
	return &Store{entries: make(map[string]*delta.Entry), max: max}, nil
}

// Apply 向 key 追加一条 delta。先整体校验后变更：任一拒绝路径不改任何状态。
func (s *Store) Apply(key string, d int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count+1 > s.max {
		return ErrCapacity
	}
	e := s.entries[key]
	if e == nil {
		e = &delta.Entry{}
		s.entries[key] = e
	}
	s.lastVisited = 0 // 纯尾部追加，不访问任何已有 delta 条目
	e.Append(d)
	s.count++
	return nil
}

// Get 返回 key 的可见值 base+Σ(delta)；未知 Key 为 0。
func (s *Store) Get(key string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e := s.entries[key]; e != nil {
		return e.Sum()
	}
	return 0
}

// Compact 把所有 Key 的未合并 delta 同时合并进 base 并清空日志。
// 全程持写锁，对外要么全合并要么全不合并，无 torn 状态；可见值不变。
func (s *Store) Compact() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		e.Compact()
	}
	s.count = 0
}

// DeltaCount 返回当前 delta 日志中的条目总数。
func (s *Store) DeltaCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

// Keys 返回当前有记录的全部 Key（供对拍测试枚举，顺序不定）。
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	return keys
}
