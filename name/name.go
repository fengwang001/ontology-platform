// Package name 提供内存中的名字集合、合法性校验与批量执行所需的互斥锁。
package name

import (
	"sort"
	"sync"
)

// Space 是一个并发安全的名字集合。零值不可用，请用 New 创建。
type Space struct {
	mu sync.RWMutex
	ns map[string]struct{}
}

// New 用初始名字集合创建命名空间。
func New(initial ...string) *Space {
	s := &Space{ns: make(map[string]struct{}, len(initial))}
	for _, n := range initial {
		s.ns[n] = struct{}{}
	}
	return s
}

// Valid 判定名字是否合法：任意字符串都合法，包括空串与含路径分隔符的串。
// 路径分隔符只按普通字符处理。
func Valid(n string) bool { return true }

// Contains 报告名字是否存在。
func (s *Space) Contains(n string) bool {
	s.mu.RLock()
	_, ok := s.ns[n]
	s.mu.RUnlock()
	return ok
}

// Len 返回名字数量。
func (s *Space) Len() int {
	s.mu.RLock()
	n := len(s.ns)
	s.mu.RUnlock()
	return n
}

// Add 加入一个名字，已存在则无变化。批量持锁期间会阻塞。
func (s *Space) Add(n string) {
	s.mu.Lock()
	s.ns[n] = struct{}{}
	s.mu.Unlock()
}

// Delete 删除一个名字，不存在则无变化。批量持锁期间会阻塞。
func (s *Space) Delete(n string) {
	s.mu.Lock()
	delete(s.ns, n)
	s.mu.Unlock()
}

// renameAtomic 在调用方已持锁的前提下执行改名；目标已存在时返回 false 且不覆盖。
func (s *Space) renameAtomic(old, nw string) bool {
	if old == nw {
		_, ok := s.ns[old]
		return ok
	}
	if _, taken := s.ns[nw]; taken {
		return false
	}
	if _, ok := s.ns[old]; !ok {
		return false
	}
	delete(s.ns, old)
	s.ns[nw] = struct{}{}
	return true
}

// Rename 改一个名字；目标已存在或旧名不存在时失败且不修改。批量持锁期间阻塞。
func (s *Space) Rename(old, nw string) bool {
	s.mu.Lock()
	ok := s.renameAtomic(old, nw)
	s.mu.Unlock()
	return ok
}

// Lock/Unlock 供批量执行器在整批执行期间独占命名空间；
// 期间外部 Add/Delete/Rename 全部阻塞（语义：阻塞而非拒绝）。
func (s *Space) Lock()   { s.mu.Lock() }
func (s *Space) Unlock() { s.mu.Unlock() }

// Move 在已持锁前提下改名，供 apply 包调用。
func (s *Space) Move(old, nw string) bool { return s.renameAtomic(old, nw) }

// Snapshot 返回当前名字集合的副本（顺序无关）。
func (s *Space) Snapshot() map[string]struct{} {
	s.mu.RLock()
	out := make(map[string]struct{}, len(s.ns))
	for n := range s.ns {
		out[n] = struct{}{}
	}
	s.mu.RUnlock()
	return out
}

// Sorted 返回字典序排列的名字列表。
func (s *Space) Sorted() []string {
	s.mu.RLock()
	out := make([]string, 0, len(s.ns))
	for n := range s.ns {
		out = append(out, n)
	}
	s.mu.RUnlock()
	sort.Strings(out)
	return out
}
