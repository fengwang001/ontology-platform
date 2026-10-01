package wal

import "container/heap"

// firstSet 是全体未落盘内存表 first 值的多重集合，
// 用最小堆 + 计数懒删除维护最小值，避免每次遍历全部内存表。
type firstSet struct {
	counts map[uint64]int // 每个 first 值对应的未落盘表数量
	h      uint64Heap     // 不同 first 值的最小堆，堆顶可能已失效
}

func newFirstSet() *firstSet {
	return &firstSet{counts: make(map[uint64]int)}
}

// add 记录一个 first 进入未落盘集合。
func (s *firstSet) add(first uint64) {
	if s.counts[first] == 0 {
		heap.Push(&s.h, first)
	}
	s.counts[first]++
}

// remove 记录一个 first 离开未落盘集合，计数为零时懒删除。
func (s *firstSet) remove(first uint64) {
	s.counts[first]--
	if s.counts[first] == 0 {
		delete(s.counts, first)
	}
}

// min 返回当前最小 first；集合为空时 ok 为 false。
func (s *firstSet) min() (min uint64, ok bool) {
	for len(s.h) > 0 {
		top := s.h[0]
		if s.counts[top] > 0 {
			return top, true
		}
		heap.Pop(&s.h)
	}
	return 0, false
}

// uint64Heap 实现 heap.Interface 的最小堆。
type uint64Heap []uint64

func (h uint64Heap) Len() int            { return len(h) }
func (h uint64Heap) Less(i, j int) bool  { return h[i] < h[j] }
func (h uint64Heap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *uint64Heap) Push(x interface{}) { *h = append(*h, x.(uint64)) }

func (h *uint64Heap) Pop() interface{} {
	old := *h
	n := len(old)
	top := old[n-1]
	*h = old[:n-1]
	return top
}
