package ontology

// entry 是水塘中保存的一条记录。
// key 是 A-ExpJ 算法为该元素计算的优先级键，
// seq 是元素到达的单调序号，用于按到达顺序输出。
type entry struct {
	item any
	key  float64
	seq  uint64
}

// minHeap 是按 key 组织的最小堆，堆顶永远是当前水塘中 key 最小的元素。
// 实现 container/heap 接口。
type minHeap []entry

func (h minHeap) Len() int { return len(h) }

func (h minHeap) Less(i, j int) bool { return h[i].key < h[j].key }

func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *minHeap) Push(x any) {
	*h = append(*h, x.(entry))
}

func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}
