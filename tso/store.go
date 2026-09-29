package tso

import "sync"

// MemoryStore 是带互斥的内存共享存储，模拟 CompareBound 的原子读后写。
type MemoryStore struct {
	mu    sync.Mutex
	bound Bound
}

// NewMemoryStore 创建空存储（任期 0、上界 0）。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (s *MemoryStore) Get() Bound {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bound
}

func (s *MemoryStore) CompareBound(decide func(current Bound) Bound) (bool, Bound) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := decide(s.bound)
	// 共享存储只接受任期不小于存量任期的原子写入。
	if next.Term < s.bound.Term {
		return false, s.bound
	}
	s.bound = next
	return true, s.bound
}
