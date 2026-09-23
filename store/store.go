// Package store 按内容地址存块、去重并维护引用计数（进程内存）。
package store

import (
	"errors"
	"sync"

	"ontology/addr"
)

var (
	// ErrNotFound 表示取回了不存在的地址。
	ErrNotFound = errors.New("store: address not found")
	// ErrTooManyChunks 表示块总数超过上限。
	ErrTooManyChunks = errors.New("store: chunk count limit exceeded")
	// ErrTooManyBytes 表示总字节数超过上限。
	ErrTooManyBytes = errors.New("store: byte limit exceeded")
)

// Limits 是存储资源上限；0 表示不限制该项。
type Limits struct {
	MaxChunks int
	MaxBytes  int64
}

type rec struct {
	data []byte
	refs int64
}

// Store 是并发安全的内容地址块存储。
type Store struct {
	mu      sync.RWMutex
	blocks  map[addr.Addr]*rec
	bytes   int64
	limits  Limits
}

// New 创建带上限的存储。
func New(limits Limits) *Store {
	return &Store{blocks: make(map[addr.Addr]*rec), limits: limits}
}

// Add 存入内容并令其引用计数 +1；内容已存在时仅增计数，不重复占用。
// 超限时返回错误且不改变任何内容与计数。
func (s *Store) Add(p []byte) (addr.Addr, error) {
	a := addr.Of(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.blocks[a]; ok {
		r.refs++
		return a, nil
	}
	if s.limits.MaxChunks > 0 && len(s.blocks) >= s.limits.MaxChunks {
		return a, ErrTooManyChunks
	}
	if s.limits.MaxBytes > 0 && s.bytes+int64(len(p)) > s.limits.MaxBytes {
		return a, ErrTooManyBytes
	}
	cp := append([]byte(nil), p...)
	s.blocks[a] = &rec{data: cp, refs: 1}
	s.bytes += int64(len(cp))
	return a, nil
}

// Get 按地址取回内容；地址不存在返回 ErrNotFound。
func (s *Store) Get(a addr.Addr) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.blocks[a]
	if !ok {
		return nil, ErrNotFound
	}
	return r.data, nil
}

// Release 令地址引用计数 -1；归零删除内容并释放字节占用。
func (s *Store) Release(a addr.Addr) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.blocks[a]
	if !ok {
		return ErrNotFound
	}
	r.refs--
	if r.refs <= 0 {
		s.bytes -= int64(len(r.data))
		delete(s.blocks, a)
	}
	return nil
}

// Refs 返回引用计数（不存在为 0）。
func (s *Store) Refs(a addr.Addr) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r, ok := s.blocks[a]; ok {
		return r.refs
	}
	return 0
}

// ChunkCount 去重后的块种数。
func (s *Store) ChunkCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.blocks)
}

// ByteCount 存储实际占用字节（零引用块为 0，因已删除）。
func (s *Store) ByteCount() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bytes
}

// Snapshot 返回“地址 -> 引用计数”的快照，供外部自检核对。
func (s *Store) Snapshot() map[addr.Addr]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[addr.Addr]int64, len(s.blocks))
	for a, r := range s.blocks {
		out[a] = r.refs
	}
	return out
}
