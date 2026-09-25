// Package store 是支持并发写入与按版本读取的键值存储。
package store

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Entry 是某键版本链上的不可变条目，Val 为只读快照语义下的字节。
type Entry struct {
	Ver int64
	Val []byte
}

// Store 保存每个键的一条版本链（下标 0 为最新版本）。
type Store struct {
	mu     sync.RWMutex
	cur    int64
	data   map[string][]*Entry
	active map[int64]int
	reads  atomic.Int64
}

// New 创建空存储。
func New() *Store {
	return &Store{data: map[string][]*Entry{}, active: map[int64]int{}}
}

// Version 返回当前版本号。
func (s *Store) Version() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Put 写入键值；存在活动快照时以追加新版本实现写时复制。
func (s *Store) Put(key string, val []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur++
	cp := append([]byte(nil), val...)
	e := &Entry{Ver: s.cur, Val: cp}
	if len(s.active) == 0 {
		s.data[key] = []*Entry{e}
		return
	}
	s.data[key] = append([]*Entry{e}, s.data[key]...)
}

// RegisterSnapshot 登记一个快照版本。
func (s *Store) RegisterSnapshot(v int64) {
	s.mu.Lock()
	s.active[v]++
	s.mu.Unlock()
}

// UnregisterSnapshot 注销快照；无活动快照时压缩全部历史。
func (s *Store) UnregisterSnapshot(v int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[v]--
	if s.active[v] == 0 {
		delete(s.active, v)
	}
	if len(s.active) != 0 {
		return
	}
	for k, chain := range s.data {
		if len(chain) > 1 {
			s.data[k] = chain[:1]
		}
	}
}

// Read 按版本读取，ok 表示该版本下键是否存在；计入读取次数。
func (s *Store) Read(v int64, key string) (val []byte, ok bool) {
	s.reads.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.data[key] {
		if e.Ver <= v {
			return e.Val, true
		}
	}
	return nil, false
}

// Keys 返回版本 v 下可见键，order 为 "asc" 或 "desc"。
func (s *Store) Keys(v int64, order string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k, chain := range s.data {
		for _, e := range chain {
			if e.Ver <= v {
				keys = append(keys, k)
				break
			}
		}
	}
	sort.Strings(keys)
	if order == "desc" {
		for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
			keys[i], keys[j] = keys[j], keys[i]
		}
	}
	return keys
}

// Retained 返回为活动快照保留的旧版本条目数。
func (s *Store) Retained() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, chain := range s.data {
		n += len(chain) - 1
	}
	return n
}

// Reads 返回自上次 ResetReads 以来的按版本读取次数。
func (s *Store) Reads() int64 { return s.reads.Load() }

// ResetReads 清零读取计数。
func (s *Store) ResetReads() { s.reads.Store(0) }
