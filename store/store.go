// Package store 是支持并发写入与单调版本号的键值存储。
package store

import "sync"

// Entry 是写入钩子看到的“被覆盖旧值”。
type Entry struct {
	Val   []byte
	Ver   uint64 // 旧值最后一次被写入的版本
	Exist bool   // false 表示该键此前不存在（用于删除/首次写入）
}

// PutHook 在每次 Put 提交后被调用，告知各快照管理器“键 key 的旧值”。
// 钩子在任何 Store 锁之外执行。
type PutHook func(key string, old Entry, newVer uint64)

// Store 是并发安全的版本化 KV。
type Store struct {
	mu      sync.RWMutex
	data    map[string][]byte
	ver     map[string]uint64
	nextVer uint64
	hook    PutHook
}

// New 创建存储，hook 可为 nil。
func New(hook PutHook) *Store {
	return &Store{data: map[string][]byte{}, ver: map[string]uint64{}, hook: hook}
}

// Put 写入或覆盖一个键，返回新版本号（从 1 开始单调递增）。
// 空键合法；空值合法，与键不存在通过 Get 的 ok 区分。
func (s *Store) Put(key string, val []byte) uint64 {
	s.mu.Lock()
	oldVal, oldExist := s.data[key]
	var oldCopy []byte
	if oldExist {
		oldCopy = append([]byte(nil), oldVal...)
	}
	s.nextVer++
	v := s.nextVer
	s.data[key] = append([]byte(nil), val...)
	s.ver[key] = v
	old := Entry{Val: oldCopy, Ver: s.ver[key], Exist: oldExist}
	if !oldExist {
		old.Ver = 0
	}
	hook := s.hook
	s.mu.Unlock()
	if hook != nil {
		hook(key, old, v)
	}
	return v
}

// Delete 删除键，返回新版本号；键不存在也推进版本。
func (s *Store) Delete(key string) uint64 {
	s.mu.Lock()
	oldVal, oldExist := s.data[key]
	var oldCopy []byte
	if oldExist {
		oldCopy = append([]byte(nil), oldVal...)
	}
	s.nextVer++
	v := s.nextVer
	delete(s.data, key)
	delete(s.ver, key)
	old := Entry{Val: oldCopy, Exist: oldExist}
	hook := s.hook
	s.mu.Unlock()
	if hook != nil {
		hook(key, old, v)
	}
	return v
}

// Get 返回当前值；ok=false 表示键不存在（空值 ok=true）。
func (s *Store) Get(key string) (val []byte, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), v...), true
}

// Len 返回当前键数。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// Snapshot 复制当前键集合（供新建快照固定键集，不含值）。
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// Version 返回当前最新版本号。
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nextVer
}
