// Package name 提供内存中的命名空间（名字集合）与合法性校验。
package name

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrExists 表示目标名已存在（不允许覆盖）。
	ErrExists = errors.New("name: destination already exists")
	// ErrMissing 表示旧名不存在。
	ErrMissing = errors.New("name: source does not exist")
)

// Space 是一个并发安全的名字集合。
type Space struct {
	mu sync.RWMutex
	ns map[string]struct{}
}

// New 用初始名字构造命名空间。
func New(initial ...string) *Space {
	s := &Space{ns: make(map[string]struct{}, len(initial))}
	for _, n := range initial {
		s.ns[n] = struct{}{}
	}
	return s
}

// Valid 报告名字是否合法：任意字符串均合法（空串与路径分隔符均可）。
func Valid(n string) bool { return true }

// Has 报告名字是否存在（内部无锁版本，供持锁调用）。
func (s *Space) Has(n string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.has(n)
}

func (s *Space) has(n string) bool { _, ok := s.ns[n]; return ok }

// Snapshot 返回当前全部名字的有序副本。
func (s *Space) Snapshot() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.ns))
	for n := range s.ns {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Len 返回名字数量。
func (s *Space) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.ns)
}

// Lock / Unlock 暴露写锁，供批量执行串行化使用。
func (s *Space) Lock()   { s.mu.Lock() }
func (s *Space) Unlock() { s.mu.Unlock() }

// MoveLocked 在已持有写锁时执行 a→b；b 已存在则失败且不修改集合。
func (s *Space) MoveLocked(a, b string) error {
	if !s.has(a) {
		return ErrMissing
	}
	if a != b {
		if s.has(b) {
			return ErrExists
		}
		delete(s.ns, a)
		s.ns[b] = struct{}{}
	}
	return nil
}

// Move 以单次写锁执行 a→b。
func (s *Space) Move(a, b string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MoveLocked(a, b)
}
