package orphanreclaim

import "container/heap"

// deadlineHeap 是某一代待回收队列的到期索引：按 deadline 升序。
// 对象在同一时刻至多属于一个代堆，杜绝「同时在第一代与第二代」的中间状态。
type deadlineHeap struct {
	order   []*objectState
	indexOf map[string]int
}

func newDeadlineHeap() *decisionDeadlineHeap {
	return &decisionDeadlineHeap{inner: &deadlineHeap{indexOf: map[string]int{}}}
}

// decisionDeadlineHeap 包装 container/heap 接口。
type decisionDeadlineHeap struct{ inner *deadlineHeap }

func (d *decisionDeadlineHeap) push(o *objectState) {
	heap.Push(d.inner, o)
}

func (d *decisionDeadlineHeap) remove(id string) bool {
	idx, ok := d.inner.indexOf[id]
	if !ok {
		return false
	}
	heap.Remove(d.inner, idx)
	return true
}

func (d *decisionDeadlineHeap) popDue(now int64) *objectState {
	if d.inner.Len() == 0 || d.inner.order[0].deadline > now {
		return nil
	}
	return heap.Pop(d.inner).(*objectState)
}

func (d *decisionDeadlineHeap) len() int { return d.inner.Len() }

func (h *deadlineHeap) Len() int { return len(h.order) }

func (h *deadlineHeap) Less(i, j int) bool {
	if h.order[i].deadline != h.order[j].deadline {
		return h.order[i].deadline < h.order[j].deadline
	}
	return h.order[i].id < h.order[j].id
}

func (h *deadlineHeap) Swap(i, j int) {
	h.order[i], h.order[j] = h.order[j], h.order[i]
	h.indexOf[h.order[i].id] = i
	h.indexOf[h.order[j].id] = j
}

func (h *deadlineHeap) Push(x any) {
	o := x.(*objectState)
	h.indexOf[o.id] = len(h.order)
	h.order = append(h.order, o)
}

func (h *deadlineHeap) Pop() any {
	n := len(h.order)
	o := h.order[n-1]
	h.order = h.order[:n-1]
	delete(h.indexOf, o.id)
	return o
}
