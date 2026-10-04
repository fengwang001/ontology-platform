package room

import "container/heap"

// expiryHeap 是按 (expiry, seq) 排序的最小堆，支撑到期的惰性落地：
// 每次操作至多弹出 expiry <= now 的记录并做一次失败的 peek，
// 检查记录数 = 本次释放组数 + 1，与房间数及未到期预留总数无关。
type expiryHeap []*reservation

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(i, j int) bool {
	if h[i].expiry != h[j].expiry {
		return h[i].expiry < h[j].expiry
	}
	return h[i].seq < h[j].seq
}

func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx = i
	h[j].heapIdx = j
}

func (h *expiryHeap) Push(x any) {
	rec := x.(*reservation)
	rec.heapIdx = len(*h)
	*h = append(*h, rec)
}

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	rec := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return rec
}

func (h *expiryHeap) push(rec *reservation) { heap.Push(h, rec) }

func (h *expiryHeap) pop() *reservation { return heap.Pop(h).(*reservation) }

// remove 按索引删除记录：预留在到期前被全部确认/离开时调用，
// 保证堆中不存在"弹出却不释放任何组"的哑记录。
func (h *expiryHeap) remove(rec *reservation) { heap.Remove(h, rec.heapIdx) }
