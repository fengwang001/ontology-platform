package cancel

import "container/heap"

// disputeState 为订单争议窗口的运行时状态。
type disputeState struct {
	active   bool
	start    int64 // 用户取消提交时刻
	deadline int64 // start + DisputeWindow，右端点本身不允许声明
	claimed  bool  // 商家是否已在窗口内声明已开始备餐
	waived   bool  // 商家是否主动放弃
	stage    Stage // 提交取消时的阶段（Accepted 或 Assigned）
}

// dueItem 是到期最小堆条目；以 deadline 为键，不维护已终结订单。
type dueItem struct {
	deadline int64
	orderID  string
	index    int
}

type dueHeap []*dueItem

func (h dueHeap) Len() int { return len(h) }
func (h dueHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].orderID < h[j].orderID
}
func (h dueHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *dueHeap) Push(x any) {
	it := x.(*dueItem)
	it.index = len(*h)
	*h = append(*h, it)
}
func (h *dueHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

var _ heap.Interface = (*dueHeap)(nil)
