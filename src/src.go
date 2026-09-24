// Package src 维护单个源分区内的元素集合。
// 每个分区是一个集合：同一元素在同一分区内至多出现一次。
// 本包不依赖其它包；并发安全由上层 uni 统一加锁保证。
package src

// Set 是单个分区的元素集合。
type Set struct {
	m map[string]struct{}
}

// New 创建一个空的分区集合。
func New() *Set {
	return &Set{m: make(map[string]struct{})}
}

// Add 把 e 加入集合。返回 true 表示本次确实新增（此前不含 e）；
// 返回 false 表示 e 已在集合中，属于幂等 no-op。
func (s *Set) Add(e string) bool {
	if _, ok := s.m[e]; ok {
		return false
	}
	s.m[e] = struct{}{}
	return true
}

// Remove 把 e 从集合移除。返回 true 表示本次确实移除（此前含 e）；
// 返回 false 表示 e 本就不在集合中，属于幂等 no-op。
func (s *Set) Remove(e string) bool {
	if _, ok := s.m[e]; !ok {
		return false
	}
	delete(s.m, e)
	return true
}

// Contains 报告 e 当前是否在该分区中。
func (s *Set) Contains(e string) bool {
	_, ok := s.m[e]
	return ok
}

// Len 返回分区内元素个数（自检用）。
func (s *Set) Len() int { return len(s.m) }

// Elements 返回集合内全部元素的快照（批量重算自检用）。
func (s *Set) Elements() []string {
	out := make([]string, 0, len(s.m))
	for e := range s.m {
		out = append(out, e)
	}
	return out
}
