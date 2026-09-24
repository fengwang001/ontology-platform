// Package store 是支持并发读写的键值存储。
// 写操作在互斥锁外通知快照钩子，使快照与 store 之间不存在反向锁序。
package store

import (
	"sort"
	"sync"
)

// WriteHook 在某次写入实际生效后被调用，old 为写入前该键的值。
// oldExists=false 表示写入前键不存在（用于登记新增键墓碑）。
type WriteHook func(key string, old []byte, oldExists bool)

type hookEntry struct {
	id uint64
	h  WriteHook
}

// Store 并发安全的内存键值存储，单调版本号随写操作递增。
type Store struct {
	mu      sync.Mutex
	data    map[string][]byte
	version uint64
	nextID  uint64
	hooks   []hookEntry
}

// New 创建空存储。
func New() *Store {
	return &Store{data: make(map[string][]byte)}
}

// Version 返回当前版本号（写入次数）。
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Put 写入键值（值被复制，调用方后续修改不影响存储）。
func (s *Store) Put(key string, value []byte) {
	s.mu.Lock()
	old, exists := s.data[key], false
	if _, ok := s.data[key]; ok {
		exists = true
	}
	if exists {
		old = append([]byte(nil), old...)
	}
	pending := make([]WriteHook, 0, len(s.hooks))
	for _, e := range s.hooks {
		pending = append(pending, e.h)
	}
	s.data[key] = cp
	s.version++
	s.mu.Unlock()
	s.notify(pending, key, old, exists)
}

// Delete 删除键；键不存在时版本号不增加，也不产生通知。
func (s *Store) Delete(key string) {
	s.mu.Lock()
	old, exists := s.data[key]
	if !exists {
		s.mu.Unlock()
		return
	}
	old = append([]byte(nil), old...)
	pending := make([]WriteHook, 0, len(s.hooks))
	for _, e := range s.hooks {
		pending = append(pending, e.h)
	}
	delete(s.data, key)
	s.version++
	s.mu.Unlock()
	s.notify(pending, key, old, true)
}

func (s *Store) notify(pending []WriteHook, key string, old []byte, exists bool) {
	for _, h := range pending {
		h(key, old, exists)
	}
}

// AddHook 注册写钩子，并在同一把锁下返回钩子 ID、注册瞬间的键集与版本，
// 保证之后任何写入都不会漏通知。
func (s *Store) AddHook(h WriteHook) (id uint64, keys []string, version uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id = s.nextID
	s.hooks = append(s.hooks, hookEntry{id: id, h: h})
	keys = make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return id, keys, s.version
}

// RemoveHook 注销写钩子。
func (s *Store) RemoveHook(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.hooks[:0]
	for _, e := range s.hooks {
		if e.id != id {
			out = append(out, e)
		}
	}
	s.hooks = out
}

// Get 读取当前值；返回值是副本。
func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), v...), true
}

// Len 返回当前键数。
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}

// Keys 返回当前全部键的字典序副本。
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
