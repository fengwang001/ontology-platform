package lessor

import "container/heap"

// leaseHeap 按 (x, id) 升序的到期最小堆。
// Renew、Revoke、Promote 改变到期时刻或删除租约后立即 Fix/Remove/Init，
// 堆中不残留过期项，不依赖惰性跳过。
type leaseHeap []*Lease

func (h leaseHeap) Len() int { return len(h) }

func (h leaseHeap) Less(i, j int) bool {
	if h[i].x != h[j].x {
		return h[i].x < h[j].x
	}
	return h[i].id < h[j].id
}

func (h leaseHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].hindex = i
	h[j].hindex = j
}

func (h *leaseHeap) Push(v any) {
	l := v.(*Lease)
	l.hindex = len(*h)
	*h = append(*h, l)
}

func (h *leaseHeap) Pop() any {
	old := *h
	n := len(old)
	l := old[n-1]
	old[n-1] = nil
	l.hindex = -1
	*h = old[:n-1]
	return l
}

func (h *leaseHeap) push(l *Lease)   { heap.Push(h, l) }
func (h *leaseHeap) pop() *Lease     { return heap.Pop(h).(*Lease) }
func (h *leaseHeap) peek() *Lease    { return (*h)[0] }
func (h *leaseHeap) remove(l *Lease) { heap.Remove(h, l.hindex) }
func (h *leaseHeap) fix(l *Lease)    { heap.Fix(h, l.hindex) }
func (h *leaseHeap) rebuild()        { heap.Init(h) }
