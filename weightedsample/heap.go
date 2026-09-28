package weightedsample

import "container/heap"

// candidate 是样本槽中的一个候选元素。
type candidate struct {
	id     string
	weight float64
	key    float64
	order  int
}

// minHeap 是按排名最低者位于堆顶的最小堆。
// 排名：键值越大越靠前；键值相同时到达序号越小越靠前。
type minHeap []*candidate

func (h minHeap) Len() int { return len(h) }

// Less 使“排名最低”者排在堆顶：键值更小者更低；键值相同则
// 到达更晚（order 更大）者更低，即并列时先到达者优先保留。
func (h minHeap) Less(i, j int) bool {
	if h[i].key != h[j].key {
		return h[i].key < h[j].key
	}
	return h[i].order > h[j].order
}

func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *minHeap) Push(x any) {
	*h = append(*h, x.(*candidate))
}

func (h *minHeap) Pop() any {
	old := *h
	last := len(old) - 1
	item := old[last]
	old[last] = nil
	*h = old[:last]
	return item
}

// worst 返回堆顶（当前样本中排名最低者）。
func (h minHeap) worst() *candidate {
	return h[0]
}

var _ heap.Interface = (*minHeap)(nil)
