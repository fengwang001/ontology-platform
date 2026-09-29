// Package store 按内容地址存块、去重并维护引用计数。
//
// 相同内容（同地址）只占一份存储，refs 记录其被块序列引用的次数；Put 原子地
// 通过容量检查后才落状态，任何拒绝都不留下半次引用（不变量 5）。
package store

import (
	"errors"
	"sync"

	"ontology/addr"
)

var (
	// ErrNotFound 在取回或释放一个不存在的地址时返回。
	ErrNotFound = errors.New("store: address not found")
	// ErrTooManyBlocks 在块种类数超出 MaxBlocks 时返回。
	ErrTooManyBlocks = errors.New("store: block count limit exceeded")
	// ErrTooManyBytes 在总字节数超出 MaxBytes 时返回。
	ErrTooManyBytes = errors.New("store: byte count limit exceeded")
)

// Limits 是可配置容量上限；零值表示该维度不限。
type Limits struct {
	MaxBlocks int
	MaxBytes  int64
}

// Store 是进程内、并发安全的块存储。
type Store struct {
	mu    sync.RWMutex
	data  map[addr.Address][]byte
	refs  map[addr.Address]int
	bytes int64
	lim   Limits
}

// New 创建带容量上限的存储。
func New(lim Limits) *Store {
	return &Store{
		data: make(map[addr.Address][]byte),
		refs: make(map[addr.Address]int),
		lim:  lim,
	}
}

// Put 按内容存入并增加一次引用；内容已存在时只加计数。超限时整体拒绝、无副作用。
func (s *Store) Put(content []byte) (addr.Address, error) {
	a := addr.Of(content)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[a]; ok {
		s.refs[a]++
		return a, nil
	}
	if s.lim.MaxBlocks > 0 && len(s.data)+1 > s.lim.MaxBlocks {
		return addr.Address{}, ErrTooManyBlocks
	}
	if s.lim.MaxBytes > 0 && s.bytes+int64(len(content)) > s.lim.MaxBytes {
		return addr.Address{}, ErrTooManyBytes
	}
	dup := make([]byte, len(content))
	copy(dup, content)
	s.data[a] = dup
	s.refs[a] = 1
	s.bytes += int64(len(content))
	return a, nil
}

// Get 按地址取回内容副本；地址不存在返回 ErrNotFound。
func (s *Store) Get(a addr.Address) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	content, ok := s.data[a]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(content))
	copy(out, content)
	return out, nil
}

// Refs 返回地址当前引用次数（不存在为 0）。
func (s *Store) Refs(a addr.Address) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.refs[a]
}

// Release 减少一次引用；计数归零则删除块与存储。地址不存在返回 ErrNotFound。
func (s *Store) Release(a addr.Address) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.refs[a]
	if !ok {
		return ErrNotFound
	}
	if n == 1 {
		s.bytes -= int64(len(s.data[a]))
		delete(s.data, a)
		delete(s.refs, a)
		return nil
	}
	s.refs[a] = n - 1
	return nil
}

// Len 返回去重后块种类数。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// Bytes 返回去重后总字节数。
func (s *Store) Bytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bytes
}

// Addresses 返回当前所有非零引用地址的快照。
func (s *Store) Addresses() []addr.Address {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]addr.Address, 0, len(s.data))
	for a := range s.data {
		out = append(out, a)
	}
	return out
}
