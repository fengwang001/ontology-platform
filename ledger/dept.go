package ledger

import "container/heap"

// 每个科室同一时刻最多允许的未结清单据数（差额待处理不计入）。
const maxOpenSlips = 3

// slipHeap 是按结清期限排序的最小堆，存放科室尚未结清
// （不含差额待处理）的单据。结清时按下标 O(log n) 移除，
// 因此锁定判定只需看堆顶，是严格的 O(1)。
type slipHeap []*slip

func (h slipHeap) Len() int { return len(h) }

func (h slipHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].id < h[j].id
}

func (h slipHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *slipHeap) Push(x any) {
	s := x.(*slip)
	s.heapIndex = len(*h)
	*h = append(*h, s)
}

func (h *slipHeap) Pop() any {
	old := *h
	n := len(old)
	s := old[n-1]
	old[n-1] = nil
	s.heapIndex = -1
	*h = old[:n-1]
	return s
}

// deptState 是科室的账册状态。
type deptState struct {
	open        slipHeap // 尚未提交结清的单据
	discrepancy int      // 差额待处理单据数
	totalSlips  int      // 历史单据总数（仅用于测试与证明）
	stats       *Stats
}

func newDeptState(stats *Stats) *deptState {
	return &deptState{stats: stats}
}

func (d *deptState) openCount() int {
	return len(d.open)
}

// locked 判定科室是否被锁定：存在逾期未结清单据或差额待处理单据。
// 只看堆顶与计数器，开销与历史单据总数无关。
func (d *deptState) locked(now int64) bool {
	if d.discrepancy > 0 {
		return true
	}
	return len(d.open) > 0 && now > d.open[0].deadline
}

func (d *deptState) addOpen(s *slip) {
	heap.Push(&d.open, s)
	d.stats.SlipHeapOps++
	d.totalSlips++
}

func (d *deptState) removeOpen(s *slip) {
	heap.Remove(&d.open, s.heapIndex)
	d.stats.SlipHeapOps++
}
