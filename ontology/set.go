package rlebitmap

import "sync"

// Set 是一个并发安全的、以规范游程形式存储的 uint32 集合。
// 零值不可直接使用，请用 NewSet 构造。
type Set struct {
	mu   sync.RWMutex
	runs []run

	// 最近一次可计数操作的统计（运算或统计）。
	last Stats
}

// NewSet 返回一个空集合。
func NewSet() *Set {
	return &Set{}
}

// Set 将位 b 置为 1。对已经为 1 的位调用是幂等的，
// 且不会改变规范编码（与未调用时逐字节相同）。
func (s *Set) Set(b uint32) {
	panic("not implemented")
}

// Clear 将位 b 置为 0。对本来就是 0 的位调用是幂等的。
func (s *Set) Clear(b uint32) {
	panic("not implemented")
}

// Contains 报告位 b 是否在集合中。
func (s *Set) Contains(b uint32) bool {
	panic("not implemented")
}

// Count 返回集合基数（置位总数），复杂度 O(游程数)。
func (s *Set) Count() uint64 {
	panic("not implemented")
}

// Min 返回最小元素；空集时 ok 为 false。复杂度 O(1)。
func (s *Set) Min() (uint32, bool) {
	panic("not implemented")
}

// Max 返回最大元素；空集时 ok 为 false。复杂度 O(1)。
func (s *Set) Max() (uint32, bool) {
	panic("not implemented")
}

// LastStats 返回最近一次可计数操作访问的游程数。
func (s *Set) LastStats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.last
}

func (s *Set) record(st Stats) {
	s.last = st
}
