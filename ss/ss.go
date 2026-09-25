// Package ss 实现 SpaceSaving 更新规则：增 / 加 / 替换、error 记账、查询与 TopK。
// 依赖 heap 做最小计数定位。
package ss

import (
	"sort"

	"ontology/heap"
)

// Entry 是一个计数器的快照。
type Entry struct {
	Key   int
	Count int
	Err   int
}

// Summary 是 SpaceSaving 结构：最多 k 个计数器。非并发安全，由上层加锁。
type Summary struct {
	h *heap.Heap
	k int
	n int // 已处理事件总数
}

// New 返回一个容量为 k 的 Summary（调用方保证 k >= 1）。
func New(k int) *Summary { return &Summary{h: heap.New(), k: k} }

// Add 处理一个事件 x：
// 已有计数器则增；未满则加；已满则替换 (Count,Key) 最小者，
// 新计数器 count = 被替换者 count + 1，error = 被替换者 count。
func (s *Summary) Add(x int) {
	s.n++
	if _, ok := s.h.Get(x); ok {
		s.h.Inc(x)
		return
	}
	if s.h.Len() < s.k {
		s.h.Add(heap.Counter{Key: x, Count: 1})
		return
	}
	old := s.h.Min()
	s.h.ReplaceMin(heap.Counter{Key: x, Count: old.Count + 1, Err: old.Count})
}

// Query 返回 x 的估计计数：有计数器返回其 count，否则返回 0。
func (s *Summary) Query(x int) int {
	if c, ok := s.h.Get(x); ok {
		return c.Count
	}
	return 0
}

// Entries 返回全部计数器，按 Count 降序、并列按 Key 升序。
func (s *Summary) Entries() []Entry {
	items := s.h.Items()
	out := make([]Entry, len(items))
	for i, c := range items {
		out[i] = Entry{Key: c.Key, Count: c.Count, Err: c.Err}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Len 返回当前计数器个数（恒 <= k）。
func (s *Summary) Len() int { return s.h.Len() }

// Total 返回已处理的事件总数。
func (s *Summary) Total() int { return s.n }
