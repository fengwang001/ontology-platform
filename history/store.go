package history

import "sync"

// Store 持有多个实例的日志，按实例字节串索引。方法并发安全。
type Store struct {
	mu sync.Mutex
	m  map[string]*Log
}

// NewStore 创建空日志存储。
func NewStore() *Store {
	return &Store{m: make(map[string]*Log)}
}

// Create 为实例建立日志；已存在时返回 false，不改变既有日志。
func (s *Store) Create(inst []byte) bool {
	key := string(inst)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; ok {
		return false
	}
	s.m[key] = &Log{}
	return true
}

// Get 返回实例的日志；不存在返回 nil。
func (s *Store) Get(inst []byte) *Log {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[string(inst)]
}
