package allocator

import "container/heap"

// splitHeap 是按拆分编号排序的索引最小堆，元素为 UNASSIGNED 状态的拆分，
// 每个偏好读取器各持有一个。堆内元素的 heapIndex 始终与其下标一致，
// 因此分配时可以用 heap.Remove 以 O(log n) 删除，不产生惰性残留。
type splitHeap []*split

func (h splitHeap) Len() int { return len(h) }

func (h splitHeap) Less(i, j int) bool { return h[i].id < h[j].id }

func (h splitHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *splitHeap) Push(x any) {
	sp := x.(*split)
	sp.heapIndex = len(*h)
	*h = append(*h, sp)
}

func (h *splitHeap) Pop() any {
	old := *h
	n := len(old)
	sp := old[n-1]
	old[n-1] = nil
	sp.heapIndex = -1
	*h = old[:n-1]
	return sp
}

// peek 返回堆顶（编号最小的候选），空堆返回 nil。
func (h *splitHeap) peek() *split {
	if len(*h) == 0 {
		return nil
	}
	return (*h)[0]
}

func (h *splitHeap) push(sp *split) {
	heap.Push(h, sp)
}

func (h *splitHeap) remove(sp *split) {
	heap.Remove(h, sp.heapIndex)
}
