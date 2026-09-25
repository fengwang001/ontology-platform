// Package dedup 维护 Seq 去重集合：成员判定、插入、已应用计数。
// 不依赖任何其他包。
package dedup

// Set 是基于哈希表的 Seq 去重集合，成员判定 O(1)。
type Set struct {
	seen map[int64]struct{}
	// lastChecked 记录最近一次成员判定检查的条目个数。
	// 非导出字段，不出现在任何公开接口；仅包内测试可直接读取。
	lastChecked int
}

// New 返回空集合。
func New() *Set { return &Set{seen: make(map[int64]struct{})} }

// Contains 判定 seq 是否已应用过，并记录本次检查的条目数。
// 哈希查找只探查一个桶位，与集合规模无关。
func (s *Set) Contains(seq int64) bool {
	s.lastChecked = 1
	_, ok := s.seen[seq]
	return ok
}

// Add 插入 seq；已存在则为幂等 no-op，返回 false 且不改变任何状态。
func (s *Set) Add(seq int64) bool {
	if s.Contains(seq) {
		return false
	}
	s.seen[seq] = struct{}{}
	return true
}

// Len 返回已应用的不同 Seq 总数。
func (s *Set) Len() int { return len(s.seen) }
