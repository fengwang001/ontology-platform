package ontology

import "container/heap"

// waitHeap 是等待分配案件的可索引最小堆，键为（受理时刻，受理序号）。
// 堆中只含等待分配的案件：案件被分配完空位或进入终态时即按索引 O(log w)
// 移除，因此申请分配取最早未分配案件的开销与已分配/已终态案件数无关。
type waitHeap struct {
	items []*caseRecord
}

func (h waitHeap) Len() int { return len(h.items) }

func (h waitHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.acceptTime != b.acceptTime {
		return a.acceptTime < b.acceptTime
	}
	return a.seq < b.seq
}

func (h waitHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].heapIndex = i
	h.items[j].heapIndex = j
}

func (h *waitHeap) Push(x any) {
	rec := x.(*caseRecord)
	rec.heapIndex = len(h.items)
	h.items = append(h.items, rec)
}

func (h *waitHeap) Pop() any {
	old := h.items
	n := len(old)
	rec := old[n-1]
	old[n-1] = nil
	rec.heapIndex = -1
	h.items = old[:n-1]
	return rec
}

func (h *waitHeap) push(rec *caseRecord) {
	heap.Push(h, rec)
}

func (h *waitHeap) remove(rec *caseRecord) {
	if rec.heapIndex >= 0 {
		heap.Remove(h, rec.heapIndex)
	}
}

func (h *waitHeap) peek() *caseRecord {
	if len(h.items) == 0 {
		return nil
	}
	return h.items[0]
}
