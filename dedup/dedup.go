// Package dedup 维护「已应用 txid」精确集合，不做容量淘汰。
package dedup

import "sort"

// Set 是已应用 txid 的精确集合。非并发安全，由调用方（apply 包）持锁保护。
type Set struct {
	m map[int64]struct{}
	// checked 记录最近一次 Seen/Add 为查重而检查过的 txid 个数。
	// 非导出，不出现在任何公开接口；仅包内白盒测试可读。
	checked int
}

// New 返回空集合。
func New() *Set { return &Set{m: make(map[int64]struct{})} }

// Seen 报告 txid 是否已在集合中。map 直接定位，只检查 1 个 txid。
func (s *Set) Seen(txid int64) bool {
	s.checked = 1
	_, ok := s.m[txid]
	return ok
}

// Add 把 txid 加入集合（重复加入无害）。
func (s *Set) Add(txid int64) {
	s.checked = 1
	s.m[txid] = struct{}{}
}

// Len 返回集合大小。
func (s *Set) Len() int { return len(s.m) }

// Snapshot 返回已应用 txid 的升序列表。
func (s *Set) Snapshot() []int64 {
	out := make([]int64, 0, len(s.m))
	for txid := range s.m {
		out = append(out, txid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
