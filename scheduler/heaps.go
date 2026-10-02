package scheduler

import "container/heap"

func (h batchHeap) Len() int { return len(h) }
func (h batchHeap) Less(i, j int) bool {
	// 所有起点互不相同（每个偏移至多领取一次）；id 仅为稳定比较。
	return h[i] < h[j]
}
func (h batchHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *batchHeap) Push(x any)   { *h = append(*h, x.(int64)) }
func (h *batchHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

var _ heap.Interface = (*batchHeap)(nil)

// cleanupPend 丢弃区间内堆顶已确认的批，每查看一项计一次探测。
// 返回最小未确认批起点（pending>0 时）；仅在 pending>0 时调用。
func (s *Scheduler) minPendingFrom(iv *interval) int64 {
	for {
		bid := iv.pendHeap[0]
		s.holdProbes++
		if !s.batches[bid].acked {
			return s.batches[bid].from
		}
		heap.Pop(&iv.pendHeap)
	}
}

// globalMin 丢弃全局堆顶的失效项（完成或版本过期），返回最小 hold。
// 没有未完成区间时第二返回值为 false。
func (s *Scheduler) globalMin() (int64, bool) {
	for s.global.Len() > 0 {
		it := s.global[0]
		s.holdProbes++
		iv := s.intervals[it.id]
		if iv != nil && !iv.done && iv.ver == it.ver {
			return it.hold, true
		}
		heap.Pop(&s.global)
	}
	return 0, false
}
