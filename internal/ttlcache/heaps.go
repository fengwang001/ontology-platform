package ttlcache

// 本文件定义 evict 使用的两个堆，均通过 container/heap 维护，
// 堆中下标缓存在 entry 内，支持 O(log n) 的定点更新与删除。

// candHeap 是驱逐候选堆，按 (writeAt 升序, tick 降序) 排序：
// 堆顶即“写入时刻最早、并列时最近使用”的项，
// 与 evict 的并列规则（并列取最近使用者）一致。
type candHeap []*entry

func (h candHeap) Len() int { return len(h) }

func (h candHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.writeAt != b.writeAt {
		return a.writeAt < b.writeAt
	}
	return a.tick > b.tick
}

func (h candHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].candIdx = i
	h[j].candIdx = j
}

func (h *candHeap) Push(x any) {
	e := x.(*entry)
	e.candIdx = len(*h)
	*h = append(*h, e)
}

func (h *candHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.candIdx = -1
	*h = old[:n-1]
	return e
}

// expHeap 按 expireAt 升序排序：堆顶是全缓存最早过期者，
// 用于 O(1) 判定“当前是否存在已过期项”。
type expHeap []*entry

func (h expHeap) Len() int { return len(h) }

func (h expHeap) Less(i, j int) bool { return h[i].expireAt < h[j].expireAt }

func (h expHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].expIdx = i
	h[j].expIdx = j
}

func (h *expHeap) Push(x any) {
	e := x.(*entry)
	e.expIdx = len(*h)
	*h = append(*h, e)
}

func (h *expHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.expIdx = -1
	*h = old[:n-1]
	return e
}
